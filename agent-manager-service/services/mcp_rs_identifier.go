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
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// ErrMCPProxyNotDeployedToEnvironment means the proxy has no deployed
// (endpoint, environment) binding to anchor a resource identifier on.
var ErrMCPProxyNotDeployedToEnvironment = errors.New("MCP proxy is not deployed to this environment")

// environmentUUIDByName resolves an environment's UUID from its name, wrapping
// a missing environment in utils.ErrEnvironmentNotFound.
func environmentUUIDByName(ctx context.Context, infra InfraResourceManager, ouID, envName string) (uuid.UUID, error) {
	envs, err := infra.ListOrgEnvironments(ctx, ouID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to list org environments: %w", err)
	}
	for _, env := range envs {
		if env.Name != envName {
			continue
		}
		envUUID, err := uuid.Parse(env.UUID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("failed to parse environment UUID %q: %w", env.UUID, err)
		}
		return envUUID, nil
	}
	return uuid.Nil, fmt.Errorf("%w: %s", utils.ErrEnvironmentNotFound, envName)
}

// EnvironmentUUIDByName resolves the named environment's UUID for the org.
// Callers deriving identifiers for several proxies in one environment resolve
// once and pass the UUID to each MCPResourceServerIdentifier call.
func (s *MCPProxyService) EnvironmentUUIDByName(ctx context.Context, ouID, envName string) (uuid.UUID, error) {
	return environmentUUIDByName(ctx, s.infraManager, ouID, envName)
}

// MCPResourceServerIdentifier derives the absolute public URI the proxy is
// invoked at in the given environment — the value the env-Thunder resource
// server's identifier must carry.
func (s *MCPProxyService) MCPResourceServerIdentifier(ctx context.Context, ouID string, envID uuid.UUID, proxy *models.MCPProxy) (string, error) {
	_ = ctx
	_, ee := resolveMCPEndpointForEnv(proxy, envID.String())
	if ee == nil || ee.ArtifactUUID == uuid.Nil {
		return "", fmt.Errorf("%w: proxy %q, environment %s", ErrMCPProxyNotDeployedToEnvironment, proxyHandleOf(proxy), envID)
	}

	deployed, err := s.deploymentRepo.GetDeployedGatewaysByProvider(ee.ArtifactUUID, ouID)
	if err != nil {
		return "", fmt.Errorf("failed to list deployed gateways for MCP artifact %s: %w", ee.ArtifactUUID, err)
	}
	gateway, err := resolveEgressGatewayForArtifact(s.gatewayRepo, ouID, envID, deployed, nil)
	if err != nil {
		if errors.Is(err, errNoGatewayForEnvironment) || errors.Is(err, errNoEgressGatewayForEnvironment) {
			return "", fmt.Errorf("%w: proxy %q, environment %s", ErrMCPProxyNotDeployedToEnvironment, proxyHandleOf(proxy), envID)
		}
		return "", err
	}

	return buildMCPProxyURL(gateway, proxy.Configuration), nil
}
