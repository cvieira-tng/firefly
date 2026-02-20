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
	"github.com/hyperledger/firefly-common/pkg/config"
	"github.com/hyperledger/firefly-common/pkg/ffresty"
	"github.com/hyperledger/firefly-common/pkg/wsclient"
)

const (
	// BsvconnectConfigKey is a sub-key in the config to contain all the bsvconnect specific config
	BsvconnectConfigKey = "bsvconnect"
	// BsvconnectConfigTopic is the topic used for operation correlation
	BsvconnectConfigTopic = "topic"
	// BsvconnectConfigBatchSize is the batch size for event delivery
	BsvconnectConfigBatchSize = "batchSize"
	// BsvconnectConfigBatchTimeout is the batch timeout for event delivery
	BsvconnectConfigBatchTimeout = "batchTimeout"

	defaultBatchSize    = 50
	defaultBatchTimeout = "500ms"
)

func (b *BSV) InitConfig(config config.Section) {
	b.bsvconnectConf = config.SubSection(BsvconnectConfigKey)
	ffresty.InitConfig(b.bsvconnectConf)
	wsclient.InitConfig(b.bsvconnectConf)
	b.bsvconnectConf.AddKnownKey(BsvconnectConfigTopic)
	b.bsvconnectConf.AddKnownKey(BsvconnectConfigBatchSize, defaultBatchSize)
	b.bsvconnectConf.AddKnownKey(BsvconnectConfigBatchTimeout, defaultBatchTimeout)
}
