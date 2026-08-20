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
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A component created before routePath existed: spec.parameters carries the
// build-time keys and nothing else.
const legacyComponentJSON = `{
  "metadata": {"name": "legacy-agent"},
  "spec": {
    "componentType": {"name": "internal-agent/agent-api"},
    "owner": {"projectName": "proj"},
    "parameters": {"exposed": true, "basePath": "/old", "port": 8080},
    "workflow": {"parameters": {}}
  }
}`

// UpdateComponentBuildParameters must never introduce routePath. A component
// without it renders the legacy "<component>-<endpoint>" path, which is the URL
// its owners already have; adding the parameter here would mean the first
// build-parameters edit silently re-points a live agent's public URL.
func TestUpdateComponentBuildParameters_DoesNotIntroduceRoutePath(t *testing.T) {
	var putParameters map[string]any

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(legacyComponentJSON))
			require.NoError(t, err)
			return
		}

		var sent struct {
			Spec struct {
				Parameters map[string]any `json:"parameters"`
			} `json:"spec"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&sent))
		putParameters = sent.Spec.Parameters

		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(legacyComponentJSON))
		require.NoError(t, err)
	}))

	err := client.UpdateComponentBuildParameters(context.Background(), "acme", "proj", "legacy-agent",
		UpdateComponentBuildParametersRequest{
			AgentType:      AgentTypeConfig{Type: "agent-api", SubType: "custom-api"},
			InputInterface: &InputInterfaceConfig{Type: "HTTP", Port: 9090, BasePath: "/new"},
		})

	require.NoError(t, err)
	require.NotNil(t, putParameters, "expected the update to PUT the component back")
	// The build-parameter writes we do expect, so a passing test can't be a
	// false negative from the update silently doing nothing.
	assert.Equal(t, "/new", putParameters["basePath"])
	assert.NotContains(t, putParameters, "routePath")
}
