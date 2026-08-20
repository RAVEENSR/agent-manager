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

package thundersvc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewThunderClientWithDialOverride_UsesOverrideAddress proves the dial-override
// constructor actually connects to resolveToHost — not the base URL's own (likely
// unreachable, e.g. *.svc.cluster.local or *.thunder.amp.localhost) host — while
// still sending the base URL's host as the HTTP Host header, so Kgateway-style
// host-based routing still selects the right backend. This is what lets
// EnvThunderResolver reach env-Thunder from a docker-compose container that can
// resolve neither the cluster-internal DNS name nor the ingress hostname directly.
func TestNewThunderClientWithDialOverride_UsesOverrideAddress(t *testing.T) {
	var gotHostHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/jwks", func(w http.ResponseWriter, r *http.Request) {
		gotHostHeader = r.Host
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	overrideHost := strings.TrimPrefix(server.URL, "http://")
	client := NewThunderClientWithDialOverride("http://unreachable.invalid:9999", "cid", "secret", overrideHost, "http://unreachable.invalid:9999/mcp")

	tc, ok := client.(*thunderClient)
	require.True(t, ok)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.baseURL+"/oauth2/jwks", nil)
	require.NoError(t, err)
	resp, err := tc.httpClient.Do(req)
	require.NoError(t, err, "must actually connect via the override address, not the unreachable base URL host")
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "unreachable.invalid:9999", gotHostHeader, "Host header must stay the base URL's host for ingress routing")
}

func TestNewThunderClientWithDialOverride_EmptyOverrideDialsBaseURLDirectly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewThunderClientWithDialOverride(server.URL, "cid", "secret", "", server.URL+"/mcp")

	tc, ok := client.(*thunderClient)
	require.True(t, ok)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.baseURL+"/oauth2/jwks", nil)
	require.NoError(t, err)
	resp, err := tc.httpClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestCreateApp_SendsRequiredApplicationType guards against a regression:
// applications require a "type" field on create, and a POST /applications
// missing it fails outright. createApp's payload must always include it.
func TestCreateApp_SendsRequiredApplicationType(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/applications", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"clientId": "amp-publisher-acme", "clientSecret": "s3cret"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewThunderClient(server.URL, "cid", "secret")
	tc, ok := client.(*thunderClient)
	require.True(t, ok)

	clientID, clientSecret, err := tc.createApp(context.Background(), "test-token", "amp-publisher-acme", "ou-1")
	require.NoError(t, err)
	assert.Equal(t, "amp-publisher-acme", clientID)
	assert.Equal(t, "s3cret", clientSecret)
	assert.Equal(t, "m2m", gotBody["type"], "type is required by ThunderID 1.0.0-alpha2 and must be sent on every application create")

	inboundAuthConfig, ok := gotBody["inboundAuthConfig"].([]any)
	require.True(t, ok)
	require.Len(t, inboundAuthConfig, 1)
	config, ok := inboundAuthConfig[0].(map[string]any)["config"].(map[string]any)
	require.True(t, ok)
	clientConfig, ok := config["token"].(map[string]any)["accessToken"].(map[string]any)["clientConfig"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []any{"ouId", "ouHandle"}, clientConfig["attributes"],
		"clientConfig.attributes must opt this client_credentials app into ouId/ouHandle token claims, or RequireOrgMatch rejects its tokens as missing ou identity")
}
