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

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

func TestMCPResourceServerIdentifier_DerivesPublicURI(t *testing.T) {
	envUUID := uuid.New()
	gwUUID := uuid.New()
	artifactUUID := uuid.New()
	ctxPath := "/github"
	proxy := &models.MCPProxy{
		UUID:          uuid.New(),
		Handle:        "gh-proxy",
		Configuration: models.MCPProxyConfig{Context: &ctxPath},
		Endpoints: []models.MCPProxyEndpoint{{
			Environments: []models.MCPProxyEndpointEnvironment{{
				EnvironmentUUID: envUUID,
				ArtifactUUID:    artifactUUID,
			}},
		}},
	}
	svc := &MCPProxyService{
		infraManager: stubInfraManager{listOrgEnvs: func(_ context.Context, _ string) ([]*models.EnvironmentResponse, error) {
			return []*models.EnvironmentResponse{{Name: "dev", UUID: envUUID.String()}}, nil
		}},
		deploymentRepo: &repomocks.DeploymentRepositoryMock{
			GetDeployedGatewaysByProviderFunc: func(providerUUID uuid.UUID, _ string) ([]string, error) {
				require.Equal(t, artifactUUID, providerUUID)
				return []string{gwUUID.String()}, nil
			},
		},
		gatewayRepo: &repomocks.GatewayRepositoryMock{
			EnvironmentMappingExistsFunc: func(gatewayID, environmentID string) (bool, error) {
				return gatewayID == gwUUID.String() && environmentID == envUUID.String(), nil
			},
			GetByUUIDFunc: func(_ string) (*models.Gateway, error) {
				return &models.Gateway{UUID: gwUUID, Vhost: "https://gw.example.com"}, nil
			},
		},
		logger: discardLogger(),
	}

	envID, err := svc.EnvironmentUUIDByName(context.Background(), "ou-1", "dev")
	require.NoError(t, err)
	require.Equal(t, envUUID, envID)

	id, err := svc.MCPResourceServerIdentifier(context.Background(), "ou-1", envID, proxy)

	require.NoError(t, err)
	require.Equal(t, "https://gw.example.com/github/mcp", id)
}

func TestMCPResourceServerIdentifier_NotDeployedToEnvironment(t *testing.T) {
	proxy := &models.MCPProxy{UUID: uuid.New(), Handle: "gh-proxy"}
	svc := &MCPProxyService{logger: discardLogger()}

	_, err := svc.MCPResourceServerIdentifier(context.Background(), "ou-1", uuid.New(), proxy)

	require.ErrorIs(t, err, ErrMCPProxyNotDeployedToEnvironment)
}

func TestEnvironmentUUIDByName_NotFound(t *testing.T) {
	svc := &MCPProxyService{
		infraManager: stubInfraManager{listOrgEnvs: func(_ context.Context, _ string) ([]*models.EnvironmentResponse, error) {
			return []*models.EnvironmentResponse{{Name: "dev", UUID: uuid.NewString()}}, nil
		}},
		logger: discardLogger(),
	}

	_, err := svc.EnvironmentUUIDByName(context.Background(), "ou-1", "prod")

	require.ErrorIs(t, err, utils.ErrEnvironmentNotFound)
}
