//
// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeAgentAPIKeySecretRef(t *testing.T) {
	envInjKey := "myagent-" + string(TraitEnvInjection)

	t.Run("nil existing returns incoming unchanged", func(t *testing.T) {
		incoming := map[string]interface{}{"other-trait": map[string]interface{}{"x": 1}}
		got := mergeAgentAPIKeySecretRef(nil, incoming, "myagent")
		assert.Equal(t, incoming, got)
	})

	t.Run("existing has no env-injection entry returns incoming unchanged", func(t *testing.T) {
		existing := map[string]interface{}{"other-trait": map[string]interface{}{}}
		incoming := map[string]interface{}{}
		got := mergeAgentAPIKeySecretRef(&existing, incoming, "myagent")
		assert.Empty(t, got)
	})

	t.Run("existing entry has empty ref returns incoming unchanged", func(t *testing.T) {
		existing := map[string]interface{}{envInjKey: map[string]interface{}{"agentApiKeySecretRef": ""}}
		incoming := map[string]interface{}{}
		got := mergeAgentAPIKeySecretRef(&existing, incoming, "myagent")
		_, present := got[envInjKey]
		assert.False(t, present)
	})

	t.Run("incoming already sets its own ref is left untouched", func(t *testing.T) {
		existing := map[string]interface{}{envInjKey: map[string]interface{}{"agentApiKeySecretRef": "old/path"}}
		incoming := map[string]interface{}{envInjKey: map[string]interface{}{"agentApiKeySecretRef": "new/path"}}
		got := mergeAgentAPIKeySecretRef(&existing, incoming, "myagent")
		entry := got[envInjKey].(map[string]interface{})
		assert.Equal(t, "new/path", entry["agentApiKeySecretRef"])
	})

	t.Run("preserves ref and property into nil incoming", func(t *testing.T) {
		existing := map[string]interface{}{
			envInjKey: map[string]interface{}{"agentApiKeySecretRef": "org/proj/prod/agent/key", "agentApiKeySecretProperty": "api-key"},
		}
		got := mergeAgentAPIKeySecretRef(&existing, nil, "myagent")
		entry := got[envInjKey].(map[string]interface{})
		assert.Equal(t, "org/proj/prod/agent/key", entry["agentApiKeySecretRef"])
		assert.Equal(t, "api-key", entry["agentApiKeySecretProperty"])
	})

	t.Run("preserves ref into incoming entry without clobbering other fields", func(t *testing.T) {
		existing := map[string]interface{}{
			envInjKey: map[string]interface{}{"agentApiKeySecretRef": "org/proj/prod/agent/key"},
		}
		incoming := map[string]interface{}{envInjKey: map[string]interface{}{"envInjectionEnabled": true}}
		got := mergeAgentAPIKeySecretRef(&existing, incoming, "myagent")
		entry := got[envInjKey].(map[string]interface{})
		assert.Equal(t, true, entry["envInjectionEnabled"])
		assert.Equal(t, "org/proj/prod/agent/key", entry["agentApiKeySecretRef"])
	})

	t.Run("incoming ref without a property is left as-is, not backfilled from a different existing secret", func(t *testing.T) {
		existing := map[string]interface{}{
			envInjKey: map[string]interface{}{"agentApiKeySecretRef": "org/proj/prod/agent/old-key", "agentApiKeySecretProperty": "api-key"},
		}
		incoming := map[string]interface{}{envInjKey: map[string]interface{}{"agentApiKeySecretRef": "org/proj/prod/agent/new-key"}}
		got := mergeAgentAPIKeySecretRef(&existing, incoming, "myagent")
		entry := got[envInjKey].(map[string]interface{})
		assert.Equal(t, "org/proj/prod/agent/new-key", entry["agentApiKeySecretRef"])
		_, present := entry["agentApiKeySecretProperty"]
		assert.False(t, present, "property must come from whoever set the new ref, never spliced from a different secret")
	})

	t.Run("a retry with a fresh existing ref is not poisoned by a prior attempt's merge", func(t *testing.T) {
		// Simulates retryReleaseBindingUpdate re-invoking its callback after a conflict: the
		// caller's incoming map must stay usable for a second merge against a newly-fetched
		// existing, not carry forward the first attempt's result.
		incoming := map[string]interface{}{}
		firstAttemptExisting := map[string]interface{}{envInjKey: map[string]interface{}{"agentApiKeySecretRef": "stale/ref"}}
		mergeAgentAPIKeySecretRef(&firstAttemptExisting, incoming, "myagent")

		concurrentlyRotatedExisting := map[string]interface{}{envInjKey: map[string]interface{}{"agentApiKeySecretRef": "fresh/ref"}}
		got := mergeAgentAPIKeySecretRef(&concurrentlyRotatedExisting, incoming, "myagent")

		entry := got[envInjKey].(map[string]interface{})
		assert.Equal(t, "fresh/ref", entry["agentApiKeySecretRef"])
	})
}
