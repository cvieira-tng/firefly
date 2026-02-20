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
	"net/http"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/hyperledger/firefly-common/pkg/config"
	"github.com/hyperledger/firefly-common/pkg/ffresty"
	"github.com/hyperledger/firefly-common/pkg/fftls"
	"github.com/hyperledger/firefly-common/pkg/fftypes"
	"github.com/hyperledger/firefly-common/pkg/wsclient"
	"github.com/hyperledger/firefly/internal/blockchain/common"
	"github.com/hyperledger/firefly/internal/coreconfig"
	"github.com/hyperledger/firefly/mocks/cachemocks"
	"github.com/hyperledger/firefly/pkg/blockchain"
	"github.com/hyperledger/firefly/pkg/core"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
)

var utConfig = config.RootSection("bsv_unit_tests")
var utBsvconnectConf = utConfig.SubSection(BsvconnectConfigKey)

func resetConf(b *BSV) {
	coreconfig.Reset()
	b.InitConfig(utConfig)
}

func newTestBSV() (*BSV, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	r := resty.New().SetBaseURL("http://localhost:12345")
	b := &BSV{
		ctx:         ctx,
		cancelCtx:   cancel,
		callbacks:   common.NewBlockchainCallbacks(),
		subs:        common.NewFireflySubscriptions(),
		client:      r,
		pluginTopic: "topic1",
		wsconns:     make(map[string]wsclient.WSClient),
		streamIDs:   make(map[string]string),
		closed:      make(map[string]chan struct{}),
	}
	return b, cancel
}

func TestName(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	assert.Equal(t, "bsv", b.Name())
}

func TestVerifierType(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	assert.Equal(t, core.VerifierTypeBSVAddress, b.VerifierType())
}

func TestInitMissingURL(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	cmi := &cachemocks.Manager{}

	err := b.Init(b.ctx, b.cancelCtx, utConfig, nil, cmi)
	assert.Regexp(t, "FF10138.*url", err)
}

func TestBadTLSConfig(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	resetConf(b)
	utBsvconnectConf.Set(ffresty.HTTPConfigURL, "http://localhost:12345")

	tlsConf := utBsvconnectConf.SubSection("tls")
	tlsConf.Set(fftls.HTTPConfTLSEnabled, true)
	tlsConf.Set(fftls.HTTPConfTLSCAFile, "!!!!!badness")

	err := b.Init(b.ctx, b.cancelCtx, utConfig, nil, &cachemocks.Manager{})
	assert.Regexp(t, "FF00153", err)
}

func TestInitMissingTopic(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	resetConf(b)
	utBsvconnectConf.Set(ffresty.HTTPConfigURL, "http://localhost:12345")

	err := b.Init(b.ctx, b.cancelCtx, utConfig, nil, &cachemocks.Manager{})
	assert.Regexp(t, "FF10138.*topic", err)
}

func TestInitSuccess(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	mockedClient := &http.Client{}
	httpmock.ActivateNonDefault(mockedClient)
	defer httpmock.DeactivateAndReset()

	resetConf(b)
	utBsvconnectConf.Set(ffresty.HTTPConfigURL, "http://localhost:12345")
	utBsvconnectConf.Set(ffresty.HTTPCustomClient, mockedClient)
	utBsvconnectConf.Set(BsvconnectConfigTopic, "topic1")

	err := b.Init(b.ctx, b.cancelCtx, utConfig, nil, &cachemocks.Manager{})
	assert.NoError(t, err)

	assert.Equal(t, "bsv", b.Name())
	assert.Equal(t, core.VerifierTypeBSVAddress, b.VerifierType())
	assert.NotNil(t, b.Capabilities())
}

func TestStopNamespace(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	// StopNamespace cleans up maps
	err := b.StopNamespace(context.Background(), "ns1")
	assert.NoError(t, err)
}

func TestVerifyBSVAddress(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	// Empty key
	_, err := b.ResolveSigningKey(context.Background(), "", blockchain.ResolveKeyIntentSign)
	assert.Regexp(t, "FF10354", err)

	// Invalid address
	_, err = b.ResolveSigningKey(context.Background(), "badaddress", blockchain.ResolveKeyIntentSign)
	assert.Regexp(t, "FF10485", err)

	// Valid mainnet address (starts with 1)
	key, err := b.ResolveSigningKey(context.Background(), "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", blockchain.ResolveKeyIntentSign)
	assert.NoError(t, err)
	assert.Equal(t, "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", key)

	// Valid mainnet address (starts with 3)
	key, err = b.ResolveSigningKey(context.Background(), "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", blockchain.ResolveKeyIntentSign)
	assert.NoError(t, err)
	assert.Equal(t, "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", key)

	// Valid testnet address (starts with m)
	key, err = b.ResolveSigningKey(context.Background(), "mipcBbFg9gMiCh81Kj8tqqdgoZub1ZJRfn", blockchain.ResolveKeyIntentSign)
	assert.NoError(t, err)
	assert.Equal(t, "mipcBbFg9gMiCh81Kj8tqqdgoZub1ZJRfn", key)

	// Valid testnet address (starts with n)
	key, err = b.ResolveSigningKey(context.Background(), "n3ZddxzLvAY9o7184TB4c6FJasAybsw4HZ", blockchain.ResolveKeyIntentSign)
	assert.NoError(t, err)
	assert.Equal(t, "n3ZddxzLvAY9o7184TB4c6FJasAybsw4HZ", key)

	// Valid testnet address (starts with 2)
	key, err = b.ResolveSigningKey(context.Background(), "2MzQwSSnBHWHqSAqtTVQ6v47XtaisrJa1Vc", blockchain.ResolveKeyIntentSign)
	assert.NoError(t, err)
	assert.Equal(t, "2MzQwSSnBHWHqSAqtTVQ6v47XtaisrJa1Vc", key)
}

func TestSubmitBatchPinSuccess(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	httpmock.ActivateNonDefault(b.client.GetClient())
	defer httpmock.DeactivateAndReset()

	httpmock.RegisterResponder("POST", "http://localhost:12345/api/v1/submit_batch_pin",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"txId": "abc123",
			"fee":  1000,
		}))

	batch := &blockchain.BatchPin{
		TransactionID:   fftypes.NewUUID(),
		BatchID:         fftypes.NewUUID(),
		BatchHash:       fftypes.NewRandB32(),
		BatchPayloadRef: "Qmf412jQZiuVUtdgnB36FXFX7xg5V6KEbSJ4dpQuhkLyfD",
		Contexts: []*fftypes.Bytes32{
			fftypes.NewRandB32(),
		},
	}

	err := b.SubmitBatchPin(context.Background(), "ns1:op1", "ns1", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", batch, nil)
	assert.NoError(t, err)
}

func TestSubmitBatchPinError(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	httpmock.ActivateNonDefault(b.client.GetClient())
	defer httpmock.DeactivateAndReset()

	httpmock.RegisterResponder("POST", "http://localhost:12345/api/v1/submit_batch_pin",
		httpmock.NewJsonResponderOrPanic(500, &common.BlockchainRESTError{
			Error: "something went wrong",
		}))

	batch := &blockchain.BatchPin{
		TransactionID:   fftypes.NewUUID(),
		BatchID:         fftypes.NewUUID(),
		BatchHash:       fftypes.NewRandB32(),
		BatchPayloadRef: "Qmf412jQZiuVUtdgnB36FXFX7xg5V6KEbSJ4dpQuhkLyfD",
		Contexts:        []*fftypes.Bytes32{},
	}

	err := b.SubmitBatchPin(context.Background(), "ns1:op1", "ns1", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", batch, nil)
	assert.Regexp(t, "FF10486", err)
}

func TestGetTransactionStatusSuccess(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	httpmock.ActivateNonDefault(b.client.GetClient())
	defer httpmock.DeactivateAndReset()

	op := &core.Operation{
		Namespace: "ns1",
		ID:        fftypes.MustParseUUID("9ffc50ff-6bfe-4502-adc7-93aea54cc059"),
		Status:    "Pending",
	}

	httpmock.RegisterResponder("POST", "http://localhost:12345/api/v1/get_receipt",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"txId":          "abc123",
			"confirmed":     true,
			"blockHeight":   100,
			"confirmations": 6,
		}))

	status, err := b.GetTransactionStatus(context.Background(), op)
	assert.NoError(t, err)
	assert.NotNil(t, status)
}

func TestGetTransactionStatusNotFound(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	httpmock.ActivateNonDefault(b.client.GetClient())
	defer httpmock.DeactivateAndReset()

	op := &core.Operation{
		Namespace: "ns1",
		ID:        fftypes.MustParseUUID("9ffc50ff-6bfe-4502-adc7-93aea54cc059"),
		Status:    "Pending",
	}

	httpmock.RegisterResponder("POST", "http://localhost:12345/api/v1/get_receipt",
		httpmock.NewStringResponder(404, "not found"))

	status, err := b.GetTransactionStatus(context.Background(), op)
	assert.NoError(t, err)
	assert.Nil(t, status)
}

func TestGetTransactionStatusError(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	httpmock.ActivateNonDefault(b.client.GetClient())
	defer httpmock.DeactivateAndReset()

	op := &core.Operation{
		Namespace: "ns1",
		ID:        fftypes.MustParseUUID("9ffc50ff-6bfe-4502-adc7-93aea54cc059"),
		Status:    "Pending",
	}

	httpmock.RegisterResponder("POST", "http://localhost:12345/api/v1/get_receipt",
		httpmock.NewStringResponder(500, "error"))

	_, err := b.GetTransactionStatus(context.Background(), op)
	assert.Regexp(t, "FF10486", err)
}

func TestSubmitNetworkActionNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	err := b.SubmitNetworkAction(b.ctx, "", "", core.NetworkActionTerminate, nil)
	assert.Regexp(t, "FF10429", err)
}

func TestDeployContractNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	rejected, err := b.DeployContract(b.ctx, "", "", nil, nil, nil, nil)
	assert.True(t, rejected)
	assert.Regexp(t, "FF10429", err)
}

func TestInvokeContractSuccess(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	httpmock.ActivateNonDefault(b.client.GetClient())
	defer httpmock.DeactivateAndReset()

	httpmock.RegisterResponder("POST", "http://localhost:12345/api/v1/invoke",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"id":   "ns1:op1",
			"txId": "abc123",
		}))

	method := &fftypes.FFIMethod{Name: "pin"}
	parsedMethod := &ffiMethodAndErrors{method: method, errors: nil}

	rejected, err := b.InvokeContract(b.ctx, "ns1:op1", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", nil, parsedMethod, map[string]interface{}{"data": "test"}, nil, nil)
	assert.False(t, rejected)
	assert.NoError(t, err)
}

func TestInvokeContractInvalidMethod(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	rejected, err := b.InvokeContract(b.ctx, "ns1:op1", "", nil, nil, nil, nil, nil)
	assert.True(t, rejected)
	assert.Regexp(t, "FF10457", err)
}

func TestQueryContractNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	_, err := b.QueryContract(b.ctx, "", nil, nil, nil, nil)
	assert.Regexp(t, "FF10429", err)
}

func TestParseInterface(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	method := &fftypes.FFIMethod{Name: "test"}
	errors := []*fftypes.FFIError{{}}

	result, err := b.ParseInterface(b.ctx, method, errors)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	parsed, ok := result.(*ffiMethodAndErrors)
	assert.True(t, ok)
	assert.Equal(t, "test", parsed.method.Name)
}

func TestValidateInvokeRequest(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	method := &fftypes.FFIMethod{Name: "test"}
	parsedMethod := &ffiMethodAndErrors{method: method, errors: nil}

	err := b.ValidateInvokeRequest(b.ctx, parsedMethod, nil, false)
	assert.NoError(t, err)

	// Test with invalid parsed method
	err = b.ValidateInvokeRequest(b.ctx, nil, nil, false)
	assert.Regexp(t, "FF10457", err)
}

func TestNormalizeContractLocationNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	_, err := b.NormalizeContractLocation(b.ctx, blockchain.NormalizeCall, nil)
	assert.Regexp(t, "FF10429", err)
}

func TestCheckOverlappingLocationsNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	_, err := b.CheckOverlappingLocations(b.ctx, nil, nil)
	assert.Regexp(t, "FF10429", err)
}

func TestAddContractListenerNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	err := b.AddContractListener(b.ctx, nil, "")
	assert.Regexp(t, "FF10429", err)
}

func TestDeleteContractListenerNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	err := b.DeleteContractListener(b.ctx, nil, false)
	assert.Regexp(t, "FF10429", err)
}

func TestGetContractListenerStatusNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	_, _, _, err := b.GetContractListenerStatus(b.ctx, "", "", false)
	assert.Regexp(t, "FF10429", err)
}

func TestGenerateEventSignatureNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	_, err := b.GenerateEventSignature(b.ctx, nil)
	assert.Regexp(t, "FF10429", err)
}

func TestGenerateEventSignatureWithLocationNotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	_, err := b.GenerateEventSignatureWithLocation(b.ctx, nil, nil)
	assert.Regexp(t, "FF10429", err)
}

func TestGenerateErrorSignature(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	sig := b.GenerateErrorSignature(b.ctx, nil)
	assert.Equal(t, "", sig)
}

func TestGenerateFFINotSupported(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	_, err := b.GenerateFFI(b.ctx, nil)
	assert.Regexp(t, "FF10347", err)
}

func TestGetNetworkVersion(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	version, err := b.GetNetworkVersion(b.ctx, nil)
	assert.NoError(t, err)
	assert.Equal(t, 2, version)
}

func TestGetAndConvertDeprecatedContractConfig(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	location, fromBlock, err := b.GetAndConvertDeprecatedContractConfig(b.ctx)
	assert.NoError(t, err)
	assert.Nil(t, location)
	assert.Equal(t, "", fromBlock)
}

func TestRemoveFireflySubscription(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	// Should not panic or error
	b.RemoveFireflySubscription(b.ctx, "test")
}

func TestGetFFIParamValidator(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	validator, err := b.GetFFIParamValidator(b.ctx)
	assert.NoError(t, err)
	assert.Nil(t, validator)
}

func TestCapabilities(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	b.capabilities = &blockchain.Capabilities{}
	caps := b.Capabilities()
	assert.NotNil(t, caps)
}

func TestRecoverFFI(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	// Test valid FFI recovery
	method := &fftypes.FFIMethod{Name: "test"}
	parsedMethod := &ffiMethodAndErrors{method: method, errors: nil}

	recoveredMethod, recoveredErrors, err := b.recoverFFI(b.ctx, parsedMethod)
	assert.NoError(t, err)
	assert.Equal(t, "test", recoveredMethod.Name)
	assert.Nil(t, recoveredErrors)

	// Test invalid FFI recovery
	_, _, err = b.recoverFFI(b.ctx, nil)
	assert.Regexp(t, "FF10457", err)

	// Test FFI with empty name
	emptyMethod := &ffiMethodAndErrors{method: &fftypes.FFIMethod{Name: ""}, errors: nil}
	_, _, err = b.recoverFFI(b.ctx, emptyMethod)
	assert.Regexp(t, "FF10457", err)
}

func TestGetTopic(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	topic := b.getTopic("ns1")
	assert.Equal(t, "topic1/ns1", topic)
}

func TestBuildEventLocationString(t *testing.T) {
	b, cancel := newTestBSV()
	defer cancel()

	msgJSON := fftypes.JSONObject{
		"transactionHash": "abc123",
	}
	location := b.buildEventLocationString(msgJSON)
	assert.Equal(t, "txid=abc123", location)
}
