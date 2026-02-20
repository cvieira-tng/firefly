// Copyright © 2025 Kaleido, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bsv

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-resty/resty/v2"
	"github.com/hyperledger/firefly-common/pkg/config"
	"github.com/hyperledger/firefly-common/pkg/ffresty"
	"github.com/hyperledger/firefly-common/pkg/fftypes"
	"github.com/hyperledger/firefly-common/pkg/i18n"
	"github.com/hyperledger/firefly-common/pkg/log"
	"github.com/hyperledger/firefly-common/pkg/wsclient"
	"github.com/hyperledger/firefly/internal/blockchain/common"
	"github.com/hyperledger/firefly/internal/cache"
	"github.com/hyperledger/firefly/internal/coremsgs"
	"github.com/hyperledger/firefly/internal/metrics"
	"github.com/hyperledger/firefly/pkg/blockchain"
	"github.com/hyperledger/firefly/pkg/core"
)

type BSV struct {
	ctx            context.Context
	cancelCtx      context.CancelFunc
	pluginTopic    string
	capabilities   *blockchain.Capabilities
	callbacks      common.BlockchainCallbacks
	client         *resty.Client
	bsvconnectConf config.Section

	// WebSocket and event streaming support
	metrics   metrics.Manager
	wsConfig  *wsclient.WSConfig
	wsconns   map[string]wsclient.WSClient
	closed    map[string]chan struct{}
	streams   *streamManager
	streamIDs map[string]string
	subs      common.FireflySubscriptions
}

// ffiMethodAndErrors stores parsed FFI method and error information
type ffiMethodAndErrors struct {
	method *fftypes.FFIMethod
	errors []*fftypes.FFIError
}

// bsvWSCommandPayload is the WebSocket command payload structure
type bsvWSCommandPayload struct {
	Type        string `json:"type"`
	Topic       string `json:"topic,omitempty"`
	BatchNumber int64  `json:"batchNumber,omitempty"`
	Message     string `json:"message,omitempty"`
}

// BSV address validation - P2PKH base58check format
// Mainnet: starts with 1 or 3
// Testnet: starts with m, n, or 2
var addressVerify = regexp.MustCompile("^[13][a-km-zA-HJ-NP-Z1-9]{25,34}$|^[mn2][a-km-zA-HJ-NP-Z1-9]{25,34}$")

func (b *BSV) Name() string {
	return "bsv"
}

func (b *BSV) VerifierType() core.VerifierType {
	return core.VerifierTypeBSVAddress
}

func (b *BSV) Init(ctx context.Context, cancelCtx context.CancelFunc, conf config.Section, metrics metrics.Manager, cacheManager cache.Manager) (err error) {
	b.InitConfig(conf)
	bsvconnectConf := b.bsvconnectConf

	b.ctx = log.WithLogField(ctx, "proto", "bsv")
	b.cancelCtx = cancelCtx
	b.metrics = metrics
	b.capabilities = &blockchain.Capabilities{}
	b.callbacks = common.NewBlockchainCallbacks()
	b.subs = common.NewFireflySubscriptions()

	if bsvconnectConf.GetString(ffresty.HTTPConfigURL) == "" {
		return i18n.NewError(ctx, coremsgs.MsgMissingPluginConfig, "url", bsvconnectConf)
	}

	// Initialize WebSocket config
	b.wsConfig, err = wsclient.GenerateConfig(ctx, bsvconnectConf)
	if err == nil {
		b.client, err = ffresty.New(b.ctx, bsvconnectConf)
	}

	if err != nil {
		return err
	}

	b.pluginTopic = bsvconnectConf.GetString(BsvconnectConfigTopic)
	if b.pluginTopic == "" {
		return i18n.NewError(ctx, coremsgs.MsgMissingPluginConfig, "topic", "blockchain.bsv.bsvconnect")
	}

	// Set default WebSocket path
	if b.wsConfig.WSKeyPath == "" {
		b.wsConfig.WSKeyPath = "/api/v1/ws"
	}

	// Initialize maps
	b.streamIDs = make(map[string]string)
	b.closed = make(map[string]chan struct{})
	b.wsconns = make(map[string]wsclient.WSClient)

	// Initialize stream manager
	b.streams = newStreamManager(
		b.client,
		b.bsvconnectConf.GetUint(BsvconnectConfigBatchSize),
		b.bsvconnectConf.GetDuration(BsvconnectConfigBatchTimeout).Milliseconds(),
	)

	return nil
}

func (b *BSV) getTopic(namespace string) string {
	return fmt.Sprintf("%s/%s", b.pluginTopic, namespace)
}

func (b *BSV) StartNamespace(ctx context.Context, namespace string) (err error) {
	logger := log.L(b.ctx)
	logger.Debugf("Starting namespace: %s", namespace)
	topic := b.getTopic(namespace)

	// Create WebSocket connection
	b.wsconns[namespace], err = wsclient.New(ctx, b.wsConfig, nil, func(ctx context.Context, w wsclient.WSClient) error {
		// Send listen command for the topic
		payload, _ := json.Marshal(&bsvWSCommandPayload{
			Type:  "listen",
			Topic: topic,
		})
		err := w.Send(ctx, payload)
		if err == nil {
			// Send listenreplies command to receive transaction receipts
			payload, _ = json.Marshal(&bsvWSCommandPayload{
				Type: "listenreplies",
			})
			err = w.Send(ctx, payload)
		}
		return err
	})
	if err != nil {
		return err
	}

	// Ensure that our event stream is in place
	stream, err := b.streams.ensureEventStream(ctx, topic)
	if err != nil {
		return err
	}
	logger.Infof("Event stream: %s (topic=%s)", stream.ID, topic)
	b.streamIDs[namespace] = stream.ID

	// Connect WebSocket
	err = b.wsconns[namespace].Connect()
	if err != nil {
		return err
	}

	b.closed[namespace] = make(chan struct{})

	// Start event loop
	go b.eventLoop(namespace)

	return nil
}

func (b *BSV) StopNamespace(ctx context.Context, namespace string) (err error) {
	wsconn, ok := b.wsconns[namespace]
	if ok {
		wsconn.Close()
	}
	delete(b.wsconns, namespace)
	delete(b.streamIDs, namespace)
	delete(b.closed, namespace)

	return nil
}

func (b *BSV) SetHandler(namespace string, handler blockchain.Callbacks) {
	b.callbacks.SetHandler(namespace, handler)
}

func (b *BSV) SetOperationHandler(namespace string, handler core.OperationCallbacks) {
	b.callbacks.SetOperationalHandler(namespace, handler)
}

func (b *BSV) Capabilities() *blockchain.Capabilities {
	return b.capabilities
}

func (b *BSV) ResolveSigningKey(ctx context.Context, key string, intent blockchain.ResolveKeyIntent) (resolved string, err error) {
	if key == "" {
		return "", i18n.NewError(ctx, coremsgs.MsgNodeMissingBlockchainKey)
	}
	resolved, err = formatBSVAddress(ctx, key)
	return resolved, err
}

func (b *BSV) SubmitBatchPin(ctx context.Context, nsOpID, networkNamespace, signingKey string, batch *blockchain.BatchPin, location *fftypes.JSONAny) error {
	// Build the contexts array as hex strings
	contexts := make([]string, len(batch.Contexts))
	for i, c := range batch.Contexts {
		contexts[i] = c.String()
	}

	payload := map[string]interface{}{
		"requestId":     nsOpID,
		"namespace":     networkNamespace,
		"transactionId": batch.TransactionID.String(),
		"batchId":       batch.BatchID.String(),
		"batchHash":     batch.BatchHash.String(),
		"payloadRef":    batch.BatchPayloadRef,
		"contexts":      contexts,
	}

	var resErr common.BlockchainRESTError
	res, err := b.client.R().
		SetContext(ctx).
		SetBody(payload).
		SetError(&resErr).
		Post("/api/v1/submit_batch_pin")
	if err != nil || !res.IsSuccess() {
		return common.WrapRESTError(ctx, &resErr, res, err, coremsgs.MsgBsvconnectRESTErr)
	}

	return nil
}

func (b *BSV) SubmitNetworkAction(ctx context.Context, nsOpID string, signingKey string, action core.NetworkActionType, location *fftypes.JSONAny) error {
	log.L(ctx).Warn("SubmitNetworkAction is not supported")
	return i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) DeployContract(ctx context.Context, nsOpID, signingKey string, definition, contract *fftypes.JSONAny, input []interface{}, options map[string]interface{}) (submissionRejected bool, err error) {
	log.L(ctx).Warn("DeployContract is not supported")
	return true, i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) ValidateInvokeRequest(ctx context.Context, parsedMethod interface{}, input map[string]interface{}, hasMessage bool) error {
	// Validate that we can recover the FFI method
	_, _, err := b.recoverFFI(ctx, parsedMethod)
	return err
}

func (b *BSV) InvokeContract(ctx context.Context, nsOpID string, signingKey string, location *fftypes.JSONAny, parsedMethod interface{}, input map[string]interface{}, options map[string]interface{}, batch *blockchain.BatchPin) (bool, error) {
	methodInfo, ok := parsedMethod.(*ffiMethodAndErrors)
	if !ok || methodInfo.method == nil {
		return true, i18n.NewError(ctx, coremsgs.MsgUnexpectedInterfaceType, parsedMethod)
	}

	body := map[string]interface{}{
		"id":     nsOpID,
		"method": methodInfo.method.Name,
		"params": input,
	}
	if signingKey != "" {
		body["from"] = signingKey
	}

	var resErr common.BlockchainRESTError
	res, err := b.client.R().
		SetContext(ctx).
		SetBody(body).
		SetError(&resErr).
		Post("/api/v1/invoke")
	if err != nil || !res.IsSuccess() {
		return resErr.SubmissionRejected, common.WrapRESTError(ctx, &resErr, res, err, coremsgs.MsgBsvconnectRESTErr)
	}
	return false, nil
}

func (b *BSV) QueryContract(ctx context.Context, signingKey string, location *fftypes.JSONAny, parsedMethod interface{}, input map[string]interface{}, options map[string]interface{}) (interface{}, error) {
	log.L(ctx).Warn("QueryContract is not supported")
	return nil, i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) ParseInterface(ctx context.Context, method *fftypes.FFIMethod, errors []*fftypes.FFIError) (interface{}, error) {
	return &ffiMethodAndErrors{
		method: method,
		errors: errors,
	}, nil
}

func (b *BSV) NormalizeContractLocation(ctx context.Context, ntype blockchain.NormalizeType, location *fftypes.JSONAny) (result *fftypes.JSONAny, err error) {
	return nil, i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) CheckOverlappingLocations(ctx context.Context, left *fftypes.JSONAny, right *fftypes.JSONAny) (bool, error) {
	return false, i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) AddContractListener(ctx context.Context, listener *core.ContractListener, lastProtocolID string) error {
	log.L(ctx).Warn("AddContractListener is not supported")
	return i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) DeleteContractListener(ctx context.Context, subscription *core.ContractListener, okNotFound bool) error {
	log.L(ctx).Warn("DeleteContractListener is not supported")
	return i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) GetContractListenerStatus(ctx context.Context, namespace, subID string, okNotFound bool) (found bool, detail interface{}, status core.ContractListenerStatus, err error) {
	return false, nil, core.ContractListenerStatusUnknown, i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) GetFFIParamValidator(ctx context.Context) (fftypes.FFIParamValidator, error) {
	return nil, nil
}

func (b *BSV) GenerateEventSignature(ctx context.Context, event *fftypes.FFIEventDefinition) (string, error) {
	return "", i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) GenerateEventSignatureWithLocation(ctx context.Context, event *fftypes.FFIEventDefinition, location *fftypes.JSONAny) (string, error) {
	return "", i18n.NewError(ctx, coremsgs.MsgNotSupportedByBlockchainPlugin)
}

func (b *BSV) GenerateErrorSignature(ctx context.Context, errorDef *fftypes.FFIErrorDefinition) string {
	return ""
}

func (b *BSV) GenerateFFI(ctx context.Context, generationRequest *fftypes.FFIGenerationRequest) (*fftypes.FFI, error) {
	return nil, i18n.NewError(ctx, coremsgs.MsgFFIGenerationUnsupported)
}

func (b *BSV) GetNetworkVersion(ctx context.Context, location *fftypes.JSONAny) (version int, err error) {
	// BSV plugin uses network version 2
	return 2, nil
}

func (b *BSV) GetAndConvertDeprecatedContractConfig(ctx context.Context) (location *fftypes.JSONAny, fromBlock string, err error) {
	return nil, "", nil
}

func (b *BSV) AddFireflySubscription(ctx context.Context, namespace *core.Namespace, contract *blockchain.MultipartyContract, lastProtocolID string) (string, error) {
	version, _ := b.GetNetworkVersion(ctx, contract.Location)

	l, err := b.streams.ensureFireFlyListener(ctx, namespace.Name, version, contract.FirstEvent, b.streamIDs[namespace.Name])
	if err != nil {
		return "", err
	}

	b.subs.AddSubscription(ctx, namespace, version, l.ID, nil)
	return l.ID, nil
}

func (b *BSV) RemoveFireflySubscription(ctx context.Context, subID string) {
	b.subs.RemoveSubscription(ctx, subID)
}

func (b *BSV) GetTransactionStatus(ctx context.Context, operation *core.Operation) (interface{}, error) {
	txnID := (&core.PreparedOperation{ID: operation.ID, Namespace: operation.Namespace}).NamespacedIDString()

	payload := map[string]interface{}{
		"txId": txnID,
	}

	var resErr common.BlockchainRESTError
	var statusResponse fftypes.JSONObject
	res, err := b.client.R().
		SetContext(ctx).
		SetBody(payload).
		SetError(&resErr).
		SetResult(&statusResponse).
		Post("/api/v1/get_receipt")
	if err != nil || !res.IsSuccess() {
		if res != nil && res.StatusCode() == 404 {
			return nil, nil
		}
		return nil, common.WrapRESTError(ctx, &resErr, res, err, coremsgs.MsgBsvconnectRESTErr)
	}

	return statusResponse, nil
}

func (b *BSV) recoverFFI(ctx context.Context, parsedMethod interface{}) (*fftypes.FFIMethod, []*fftypes.FFIError, error) {
	methodInfo, ok := parsedMethod.(*ffiMethodAndErrors)
	if !ok || methodInfo.method == nil || methodInfo.method.Name == "" {
		return nil, nil, i18n.NewError(ctx, coremsgs.MsgUnexpectedInterfaceType, parsedMethod)
	}
	return methodInfo.method, methodInfo.errors, nil
}

func (b *BSV) eventLoop(namespace string) {
	topic := b.getTopic(namespace)
	wsconn := b.wsconns[namespace]
	closed := b.closed[namespace]

	defer wsconn.Close()
	defer close(closed)
	l := log.L(b.ctx).WithField("role", "event-loop")
	ctx := log.WithLogger(b.ctx, l)
	for {
		select {
		case <-ctx.Done():
			l.Debugf("Event loop exiting (context cancelled)")
			return
		case msgBytes, ok := <-wsconn.Receive():
			if !ok {
				l.Debugf("Event loop exiting (receive channel closed). Terminating server!")
				b.cancelCtx()
				return
			}

			var msgParsed interface{}
			err := json.Unmarshal(msgBytes, &msgParsed)
			if err != nil {
				l.Errorf("Message cannot be parsed as JSON: %s\n%s", err, string(msgBytes))
				continue // Swallow this and move on
			}
			switch msgTyped := msgParsed.(type) {
			case []interface{}:
				err = b.handleMessageBatch(ctx, namespace, 0, msgTyped)
				if err == nil {
					ack, _ := json.Marshal(&bsvWSCommandPayload{
						Type:  "ack",
						Topic: topic,
					})
					err = wsconn.Send(ctx, ack)
				}
			case map[string]interface{}:
				if batchNumber, ok := msgTyped["batchNumber"].(float64); ok {
					if events, ok := msgTyped["events"].([]interface{}); ok {
						// FFTM delivery with a batch number to use in the ack
						err = b.handleMessageBatch(ctx, namespace, (int64)(batchNumber), events)
						// Errors processing messages are converted into nacks
						ackOrNack := &bsvWSCommandPayload{
							Topic:       topic,
							BatchNumber: int64(batchNumber),
						}
						if err == nil {
							ackOrNack.Type = "ack"
						} else {
							log.L(ctx).Errorf("Rejecting batch due error: %s", err)
							ackOrNack.Type = "error"
							ackOrNack.Message = err.Error()
						}
						payload, _ := json.Marshal(&ackOrNack)
						err = wsconn.Send(ctx, payload)
					}
				} else if msgType, ok := msgTyped["type"].(string); ok && msgType == "Receipt" {
					// Single receipt message (not in a batch)
					err = b.handleMessageBatch(ctx, namespace, 0, []interface{}{msgTyped})
				} else {
					l.Errorf("Message unexpected: %+v", msgTyped)
				}
			default:
				l.Errorf("Message unexpected: %+v", msgTyped)
				continue
			}

			if err != nil {
				l.Errorf("Event loop exiting (%s). Terminating server!", err)
				b.cancelCtx()
				return
			}
		}
	}
}

func (b *BSV) handleMessageBatch(ctx context.Context, namespace string, batchID int64, messages []interface{}) error {
	events := make(common.EventsToDispatch)
	updates := make([]*core.OperationUpdate, 0)
	count := len(messages)
	for i, msgI := range messages {
		msgMap, ok := msgI.(map[string]interface{})
		if !ok {
			log.L(ctx).Errorf("Message cannot be parsed as JSON: %+v", msgI)
			return nil
		}
		msgJSON := fftypes.JSONObject(msgMap)

		switch msgJSON.GetString("type") {
		case "ContractEvent":
			signature := msgJSON.GetString("signature")

			logger := log.L(ctx)
			logger.Infof("[%d:%d/%d]: '%s'", batchID, i+1, count, signature)
			logger.Tracef("Message: %+v", msgJSON)
			b.processContractEvent(ctx, namespace, events, msgJSON)
		case "Receipt":
			var receipt common.BlockchainReceiptNotification
			msgBytes, _ := json.Marshal(msgMap)
			_ = json.Unmarshal(msgBytes, &receipt)

			err := common.AddReceiptToBatch(ctx, namespace, b, &receipt, &updates)
			if err != nil {
				log.L(ctx).Errorf("Failed to process receipt: %+v", msgMap)
			}
		default:
			log.L(ctx).Errorf("Unexpected message in batch: %+v", msgMap)
		}

	}

	if len(updates) > 0 {
		err := b.callbacks.BulkOperationUpdates(ctx, namespace, updates)
		if err != nil {
			return err
		}
	}
	// Dispatch all the events from this patch that were successfully parsed and routed to namespaces
	return b.callbacks.DispatchBlockchainEvents(ctx, events)
}

func (b *BSV) processContractEvent(ctx context.Context, namespace string, events common.EventsToDispatch, msgJSON fftypes.JSONObject) {
	listenerID := msgJSON.GetString("listenerId")
	event := b.parseBlockchainEvent(ctx, msgJSON)
	if event != nil {
		b.callbacks.PrepareBlockchainEvent(ctx, events, namespace, &blockchain.EventForListener{
			Event:      event,
			ListenerID: listenerID,
		})
	}
}

func (b *BSV) parseBlockchainEvent(ctx context.Context, msgJSON fftypes.JSONObject) *blockchain.Event {
	sBlockNumber := msgJSON.GetString("blockNumber")
	sTransactionHash := msgJSON.GetString("transactionHash")
	blockNumber := msgJSON.GetInt64("blockNumber")
	txIndex := msgJSON.GetInt64("transactionIndex")
	logIndex := msgJSON.GetInt64("logIndex")
	dataJSON := msgJSON.GetObject("data")
	signature := msgJSON.GetString("signature")
	name := strings.SplitN(signature, "(", 2)[0]
	timestampStr := msgJSON.GetString("timestamp")
	timestamp, err := fftypes.ParseTimeString(timestampStr)
	if err != nil {
		log.L(ctx).Errorf("Blockchain event is not valid - missing timestamp: %+v", msgJSON)
		return nil // move on
	}

	if sBlockNumber == "" || sTransactionHash == "" {
		log.L(ctx).Errorf("Blockchain event is not valid - missing data: %+v", msgJSON)
		return nil // move on
	}

	delete(msgJSON, "data")
	return &blockchain.Event{
		BlockchainTXID: sTransactionHash,
		Source:         b.Name(),
		Name:           name,
		ProtocolID:     fmt.Sprintf("%.12d/%.6d/%.6d", blockNumber, txIndex, logIndex),
		Output:         dataJSON,
		Info:           msgJSON,
		Timestamp:      timestamp,
		Location:       b.buildEventLocationString(msgJSON),
		Signature:      signature,
	}
}

func (b *BSV) buildEventLocationString(msgJSON fftypes.JSONObject) string {
	return fmt.Sprintf("txid=%s", msgJSON.GetString("transactionHash"))
}

func formatBSVAddress(ctx context.Context, key string) (string, error) {
	// Validate BSV address format (P2PKH base58check)
	if addressVerify.MatchString(key) {
		return key, nil
	}
	return "", i18n.NewError(ctx, coremsgs.MsgInvalidBSVAddress)
}
