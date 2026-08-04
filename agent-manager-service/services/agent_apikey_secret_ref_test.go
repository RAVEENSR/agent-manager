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

package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/client"
	"github.com/wso2/agent-manager/agent-manager-service/clients/secretmanagersvc"
)

func TestInjectAgentAPIKeySecretRef_AddsEntry(t *testing.T) {
	traitEnvConfigs := map[string]interface{}{}

	injectAgentAPIKeySecretRef(traitEnvConfigs, "myagent", "kv/prod/path", "api-key")

	entry, ok := traitEnvConfigs["myagent-"+string(client.TraitEnvInjection)].(map[string]interface{})
	require.True(t, ok, "env-injection entry must be created")
	assert.Equal(t, "kv/prod/path", entry["agentApiKeySecretRef"])
	assert.Equal(t, "api-key", entry["agentApiKeySecretProperty"])
}

func TestInjectAgentAPIKeySecretRef_PreservesExistingConfig(t *testing.T) {
	envInjKey := "myagent-" + string(client.TraitEnvInjection)
	traitEnvConfigs := map[string]interface{}{
		envInjKey: map[string]interface{}{"envInjectionEnabled": true},
	}

	injectAgentAPIKeySecretRef(traitEnvConfigs, "myagent", "kv/prod/path", "api-key")

	entry := traitEnvConfigs[envInjKey].(map[string]interface{})
	assert.Equal(t, true, entry["envInjectionEnabled"], "pre-existing config must survive")
	assert.Equal(t, "kv/prod/path", entry["agentApiKeySecretRef"])
}

func TestInjectAgentAPIKeySecretRef_NoopOnEmptyRef(t *testing.T) {
	traitEnvConfigs := map[string]interface{}{}

	injectAgentAPIKeySecretRef(traitEnvConfigs, "myagent", "", "")

	assert.Empty(t, traitEnvConfigs, "an empty ref must not add an entry (would clobber the base fallback)")
}

func TestStoreAgentAPIKey_ResolvesRemoteRefAfterCreate(t *testing.T) {
	ocClient := &clientmocks.OpenChoreoClientMock{
		GetSecretReferenceFunc: func(_ context.Context, _, secretRefName string) (*client.SecretReferenceInfo, error) {
			return &client.SecretReferenceInfo{
				Name: secretRefName,
				Data: []client.SecretDataSourceInfo{
					{SecretKey: secretmanagersvc.SecretKeyAPIKey, RemoteRef: client.RemoteRefInfo{
						Key: "org/proj/production/agent/agent-agent-api-key", Property: "api-key",
					}},
				},
			}, nil
		},
	}
	secretMgmtClient := &clientmocks.SecretManagementClientMock{
		CreateSecretFunc: func(_ context.Context, location secretmanagersvc.SecretLocation, _ map[string]string) (string, error) {
			return location.SecretRefName(), nil
		},
	}
	s := &agentManagerService{ocClient: ocClient, secretMgmtClient: secretMgmtClient, logger: discardLogger()}

	ref, property, err := s.storeAgentAPIKey(context.Background(), "org", "proj", "agent", "production", "the-api-key")

	require.NoError(t, err)
	assert.Equal(t, "org/proj/production/agent/agent-agent-api-key", ref)
	assert.Equal(t, "api-key", property)
	// The lookup must key off the deterministic per-env SecretReference name returned by CreateSecret.
	require.Len(t, ocClient.GetSecretReferenceCalls(), 1)
	expectedName := agentAPIKeySecretLocation("org", "proj", "agent", "production").SecretRefName()
	assert.Equal(t, expectedName, ocClient.GetSecretReferenceCalls()[0].SecretRefName)
}
