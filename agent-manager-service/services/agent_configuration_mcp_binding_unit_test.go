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

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// mcpVarRows builds the pair of env var rows (url + apikey) that configuring an MCP
// connection persists for an environment, whether or not that environment turned out
// to be deployable.
func mcpVarRows(configUUID uuid.UUID, envUUIDs ...uuid.UUID) []models.AgentEnvConfigVariable {
	rows := make([]models.AgentEnvConfigVariable, 0, len(envUUIDs)*2)
	for _, envUUID := range envUUIDs {
		rows = append(
			rows,
			models.AgentEnvConfigVariable{ConfigUUID: configUUID, EnvironmentUUID: envUUID, VariableKey: "url", VariableName: "BOOKING_URL"},
			models.AgentEnvConfigVariable{ConfigUUID: configUUID, EnvironmentUUID: envUUID, VariableKey: "apikey", VariableName: "BOOKING_API_KEY"},
		)
	}
	return rows
}

// An environment the connection was configured for, but which never got a mapping
// because the proxy had no endpoint bound there at the time, must be reported as
// needing activation. This is the state promotion leaves behind: env var rows exist
// (so the vars are injected) but they resolve to empty strings forever.
func TestMCPEnvsNeedingActivation_ReportsEnvWithVarRowsButNoMapping(t *testing.T) {
	configUUID, proxyUUID := uuid.New(), uuid.New()
	devEnv, prodEnv := uuid.New(), uuid.New()

	mappings := []models.EnvAgentMCPMapping{
		{ConfigUUID: configUUID, EnvironmentUUID: devEnv, MCPProxyUUID: proxyUUID},
	}
	vars := mcpVarRows(configUUID, devEnv, prodEnv)

	got := mcpEnvsNeedingActivation(mappings, vars, proxyUUID)

	require.Equal(t, []uuid.UUID{prodEnv}, got,
		"prod has env var rows but no mapping — it must be reported for backfill")
}

// Each unmapped environment is reported once, in the order its variable rows appear —
// mcpVarRows emits two rows per environment, and a repeated environment would make the
// caller activate it twice and violate uq_env_mcp_mapping on the second pass.
func TestMCPEnvsNeedingActivation_ReportsEachUnmappedEnvOnce(t *testing.T) {
	configUUID, proxyUUID := uuid.New(), uuid.New()
	devEnv, stagingEnv, prodEnv := uuid.New(), uuid.New(), uuid.New()

	mappings := []models.EnvAgentMCPMapping{
		{ConfigUUID: configUUID, EnvironmentUUID: devEnv, MCPProxyUUID: proxyUUID},
	}
	vars := mcpVarRows(configUUID, devEnv, stagingEnv, prodEnv)

	got := mcpEnvsNeedingActivation(mappings, vars, proxyUUID)

	require.Equal(t, []uuid.UUID{stagingEnv, prodEnv}, got)
}

// An environment that already has a mapping is fully bound; re-activating it would
// mint a duplicate API key and violate uq_env_mcp_mapping. An environment that was never
// configured is absent from both slices, so it is covered by the same assertion.
func TestMCPEnvsNeedingActivation_SkipsAlreadyMappedEnv(t *testing.T) {
	configUUID, proxyUUID := uuid.New(), uuid.New()
	devEnv := uuid.New()

	mappings := []models.EnvAgentMCPMapping{
		{ConfigUUID: configUUID, EnvironmentUUID: devEnv, MCPProxyUUID: proxyUUID},
	}

	got := mcpEnvsNeedingActivation(mappings, mcpVarRows(configUUID, devEnv), proxyUUID)

	require.Empty(t, got)
}

// The proxy to bind in an unmapped environment is inferred from the config's sibling
// environments. That inference is only sound when every existing mapping names the
// same proxy — a config deliberately bound to different proxies per environment
// records no intent for the unmapped one, so guessing would bind the wrong proxy.
func TestMCPEnvsNeedingActivation_SkipsConfigBoundToMultipleProxies(t *testing.T) {
	configUUID := uuid.New()
	proxyUUID, otherProxyUUID := uuid.New(), uuid.New()
	devEnv, stagingEnv, prodEnv := uuid.New(), uuid.New(), uuid.New()

	mappings := []models.EnvAgentMCPMapping{
		{ConfigUUID: configUUID, EnvironmentUUID: devEnv, MCPProxyUUID: proxyUUID},
		{ConfigUUID: configUUID, EnvironmentUUID: stagingEnv, MCPProxyUUID: otherProxyUUID},
	}
	vars := mcpVarRows(configUUID, devEnv, stagingEnv, prodEnv)

	got := mcpEnvsNeedingActivation(mappings, vars, proxyUUID)

	require.Empty(t, got, "ambiguous proxy intent must not be guessed")
}

// Backfill is driven from one proxy's update, so a config that has no mapping to that
// proxy at all is none of this proxy's business.
func TestMCPEnvsNeedingActivation_SkipsConfigNotBoundToThisProxy(t *testing.T) {
	configUUID := uuid.New()
	proxyUUID, otherProxyUUID := uuid.New(), uuid.New()
	devEnv, prodEnv := uuid.New(), uuid.New()

	mappings := []models.EnvAgentMCPMapping{
		{ConfigUUID: configUUID, EnvironmentUUID: devEnv, MCPProxyUUID: otherProxyUUID},
	}

	got := mcpEnvsNeedingActivation(mappings, mcpVarRows(configUUID, devEnv, prodEnv), proxyUUID)

	require.Empty(t, got)
}

// unresolvedBindingsFixture builds the service ListUnresolvedMCPBindings needs: an
// environment lookup, the agent's configurations, and their per-environment variable rows.
// No configuration has an MCP mapping in the environment — the dead state promotion must
// refuse — so the URL each one resolves to is empty.
func unresolvedBindingsFixture(
	envUUID uuid.UUID,
	configs []models.AgentConfiguration,
	varsByConfig map[uuid.UUID][]models.AgentEnvConfigVariable,
) *agentConfigurationService {
	return &agentConfigurationService{
		ocClient: &clientmocks.OpenChoreoClientMock{
			GetEnvironmentFunc: func(_ context.Context, _, envName string) (*models.EnvironmentResponse, error) {
				return &models.EnvironmentResponse{Name: envName, UUID: envUUID.String()}, nil
			},
		},
		agentConfigRepo: &repomocks.AgentConfigurationRepositoryMock{
			ListByAgentFunc: func(_ context.Context, _, _, _ string, _, _ int) ([]models.AgentConfiguration, error) {
				return configs, nil
			},
		},
		envVariableRepo: &repomocks.AgentEnvConfigVariableRepositoryMock{
			ListByConfigAndEnvFunc: func(_ context.Context, configUUID, _ uuid.UUID) ([]models.AgentEnvConfigVariable, error) {
				return varsByConfig[configUUID], nil
			},
		},
		envMCPMappingRepo: &repomocks.EnvAgentMCPMappingRepositoryMock{
			ListByConfigFunc: func(_ context.Context, _ uuid.UUID) ([]models.EnvAgentMCPMapping, error) {
				return []models.EnvAgentMCPMapping{}, nil
			},
		},
	}
}

// A connection configured for the environment — its variable rows are there, so its URL and
// API key are injected into the workload — but with no mapping backing them resolves to an
// empty URL. That is the connection promotion must refuse to carry over.
func TestListUnresolvedMCPBindings_ReportsConfiguredConnectionWithNoResolvableURL(t *testing.T) {
	envUUID := uuid.New()
	bookingUUID := uuid.New()
	configs := []models.AgentConfiguration{
		{UUID: bookingUUID, Name: "booking", TypeID: models.AgentConfigTypeIDMCP},
	}
	varsByConfig := map[uuid.UUID][]models.AgentEnvConfigVariable{
		bookingUUID: mcpVarRows(bookingUUID, envUUID),
	}

	svc := unresolvedBindingsFixture(envUUID, configs, varsByConfig)

	got, err := svc.ListUnresolvedMCPBindings(context.Background(), "my-agent", "acme", "proj1", "staging")

	require.NoError(t, err)
	require.Equal(t, map[string]struct{}{"booking": {}}, got)
}

// No variable rows in the environment means the connection was never offered there, so
// nothing is injected and nothing is broken. Reporting it would block promotions that are
// perfectly safe.
func TestListUnresolvedMCPBindings_SkipsConnectionNotConfiguredForEnvironment(t *testing.T) {
	envUUID := uuid.New()
	bookingUUID := uuid.New()
	configs := []models.AgentConfiguration{
		{UUID: bookingUUID, Name: "booking", TypeID: models.AgentConfigTypeIDMCP},
	}

	svc := unresolvedBindingsFixture(envUUID, configs, nil)

	got, err := svc.ListUnresolvedMCPBindings(context.Background(), "my-agent", "acme", "proj1", "staging")

	require.NoError(t, err)
	require.Empty(t, got)
}

// An LLM configuration also owns injected system-managed variables, but its URL comes from
// the provider rather than an MCP mapping. Scanning it here would report every LLM binding
// as a broken MCP connection.
func TestListUnresolvedMCPBindings_IgnoresNonMCPConfigurations(t *testing.T) {
	envUUID := uuid.New()
	llmUUID := uuid.New()
	configs := []models.AgentConfiguration{
		{UUID: llmUUID, Name: "openai", TypeID: models.AgentConfigTypeIDLLM},
	}
	varsByConfig := map[uuid.UUID][]models.AgentEnvConfigVariable{
		llmUUID: mcpVarRows(llmUUID, envUUID),
	}

	svc := unresolvedBindingsFixture(envUUID, configs, varsByConfig)

	got, err := svc.ListUnresolvedMCPBindings(context.Background(), "my-agent", "acme", "proj1", "staging")

	require.NoError(t, err)
	require.Empty(t, got)
}
