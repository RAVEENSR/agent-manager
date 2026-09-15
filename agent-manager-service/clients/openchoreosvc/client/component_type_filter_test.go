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

	"github.com/wso2/agent-manager/agent-manager-service/clients/openchoreosvc/gen"
)

// The component types below are the ones observed on real shared projects. Only the two
// agent types are agent-manager's own; every other product's components must stay out of
// the agent list, including the ones whose names read like agents.
func TestIsAgentComponentType(t *testing.T) {
	agentTypes := []string{
		"proxy/agent-api",
		"proxy/external-agent-api",
	}
	for _, componentType := range agentTypes {
		assert.True(t, isAgentComponentType(componentType), "expected %q to be an agent component type", componentType)
	}

	foreignTypes := []string{
		"deployment/service",
		"deployment/web-application",
		"deployment/code-server",
		"deployment/event-integration",
		"deployment/file-integration",
		"deployment/integration-as-api",
		"cronjob/scheduled-task",
		// Named like agents, but created by other products, not by agent-manager.
		"deployment/ai-agent",
		"deployment/pa-service",
		"job/coding-agent",
		// Shares the "proxy/" prefix, so a prefix match would wrongly accept it.
		"proxy/proxy-api",
		// The unqualified form is not what OpenChoreo stores on a Component.
		"agent-api",
		"",
	}
	for _, componentType := range foreignTypes {
		assert.False(t, isAgentComponentType(componentType), "expected %q not to be an agent component type", componentType)
	}
}

func TestCountProjectComponents_FollowsPagination(t *testing.T) {
	requests := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "shared-project", r.URL.Query().Get("project"))
		require.Equal(t, "100", r.URL.Query().Get("limit"))

		page := gen.ComponentList{Items: make([]gen.Component, 37)}
		if requests == 1 {
			require.Empty(t, r.URL.Query().Get("cursor"))
			page.Items = make([]gen.Component, defaultListLimit)
			next := "page-2"
			page.Pagination.NextCursor = &next
		} else {
			require.Equal(t, "page-2", r.URL.Query().Get("cursor"))
		}

		require.NoError(t, json.NewEncoder(w).Encode(page))
	}))

	count, err := c.CountProjectComponents(context.Background(), "ou-acme", "shared-project")

	require.NoError(t, err)
	assert.Equal(t, 137, count)
	assert.Equal(t, 2, requests)
}
