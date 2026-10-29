//go:build integration

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

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/jwtassertion"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/tests/apitestutils"
	"github.com/wso2/agent-manager/agent-manager-service/wiring"
)

var (
	a2aLifecycleOrgName   = fmt.Sprintf("a2a-lc-org-%s", uuid.New().String()[:5])
	a2aLifecycleProjName  = fmt.Sprintf("a2a-lc-proj-%s", uuid.New().String()[:5])
	a2aLifecycleAgentName = fmt.Sprintf("a2a-lc-agent-%s", uuid.New().String()[:5])
)

// seedIngressGateway inserts a gateway and maps it to the environment, because
// an A2A agent is published to its environment's INGRESS gateway and the test
// database carries none by default.
func seedIngressGateway(t *testing.T, ouID string, environmentUUID uuid.UUID) uuid.UUID {
	t.Helper()
	gdb := db.GetDB()
	gatewayID := uuid.New()
	name := "a2a-lc-gw-" + gatewayID.String()[:8]
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)`,
		gatewayID, name, name, "agents.example.com", models.GatewayRoleIngress, ouID,
	).Error)
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateway_environment_mappings (gateway_uuid, environment_uuid) VALUES (?, ?)`,
		gatewayID, environmentUUID,
	).Error)
	t.Cleanup(func() {
		gdb.Exec(`DELETE FROM gateway_environment_mappings WHERE gateway_uuid = ?`, gatewayID)
		gdb.Exec(`DELETE FROM gateways WHERE uuid = ?`, gatewayID)
	})
	return gatewayID
}

// TestA2AAgentLifecycle drives create -> deploy -> deploy settings -> delete for an a2a-agent and checks the
// record that says the agent is exposed through its environment's gateway.
func TestA2AAgentLifecycle(t *testing.T) {
	authMiddleware := jwtassertion.NewMockMiddleware(t)

	environmentUUID := uuid.New()

	openChoreoClient := apitestutils.CreateMockOpenChoreoClient()
	openChoreoClient.ComponentExistsFunc = func(ctx context.Context, orgName, projName, agentName string) (bool, error) {
		return true, nil
	}
	// Deploy reads the agent's subtype off the component OpenChoreo reports, and
	// the default mock reports none — so a deploy would take the REST path and
	// never record an exposure.
	openChoreoClient.GetComponentFunc = func(ctx context.Context, namespaceName, projectName, componentName string) (*models.AgentResponse, error) {
		return &models.AgentResponse{
			UUID:         uuid.New().String(),
			Name:         componentName,
			ProjectName:  projectName,
			Provisioning: models.Provisioning{Type: "internal"},
			Type:         models.AgentType{Type: "agent-api", SubType: "a2a-agent"},
			CreatedAt:    time.Now(),
		}, nil
	}
	// The default mock returns a non-UUID environment id; the exposure is keyed
	// on the environment UUID, so this test needs a real one.
	openChoreoClient.GetEnvironmentFunc = func(ctx context.Context, namespaceName, environmentName string) (*models.EnvironmentResponse, error) {
		return &models.EnvironmentResponse{UUID: environmentUUID.String(), Name: environmentName}, nil
	}

	app := apitestutils.MakeAppClientWithDeps(t, wiring.TestClients{
		OpenChoreoClient: openChoreoClient,
		SecretMgmtClient: apitestutils.CreateMockSecretManagementClient(),
	}, authMiddleware)

	gdb := db.GetDB()
	exposureRepo := repositories.NewPlatformAgentExposureRepository(gdb)
	artifactRepo := repositories.NewArtifactRepo(gdb)
	ouID := "mock-org-id"

	seedIngressGateway(t, ouID, environmentUUID)

	t.Cleanup(func() {
		_ = exposureRepo.DeleteForAgent(context.Background(), ouID, a2aLifecycleProjName, a2aLifecycleAgentName)
	})

	// exposure returns the agent's exposure in the environment, or nil.
	exposure := func(t *testing.T) *models.PlatformAgentExposure {
		t.Helper()
		row, err := exposureRepo.Get(context.Background(), ouID, a2aLifecycleProjName, a2aLifecycleAgentName, environmentUUID)
		if err != nil {
			return nil
		}
		return row
	}

	deployAgent := func(t *testing.T) {
		t.Helper()
		body := new(bytes.Buffer)
		require.NoError(t, json.NewEncoder(body).Encode(map[string]interface{}{
			"imageId": "registry.example.com/a2a:v1.0.0",
		}))
		url := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s/deployments",
			a2aLifecycleOrgName, a2aLifecycleProjName, a2aLifecycleAgentName)
		req := httptest.NewRequest(http.MethodPost, url, body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)
		require.Equal(t, http.StatusAccepted, rr.Code, "body: %s", rr.Body.String())
	}

	t.Run("create an a2a agent", func(t *testing.T) {
		body := new(bytes.Buffer)
		require.NoError(t, json.NewEncoder(body).Encode(map[string]interface{}{
			"name":        a2aLifecycleAgentName,
			"displayName": "A2A Lifecycle Agent",
			"description": "Drives the full A2A lifecycle",
			"provisioning": map[string]interface{}{
				"type": "internal",
				"repository": map[string]interface{}{
					"url":     "https://github.com/example/a2a-agent",
					"branch":  "main",
					"appPath": "/",
				},
			},
			"agentType": map[string]interface{}{"type": "agent-api", "subType": "a2a-agent"},
			"build": map[string]interface{}{
				"type":   "docker",
				"docker": map[string]interface{}{"dockerfilePath": "/Dockerfile"},
			},
			"inputInterface": map[string]interface{}{"type": "HTTP", "port": 9099},
		}))
		url := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents", a2aLifecycleOrgName, a2aLifecycleProjName)
		req := httptest.NewRequest(http.MethodPost, url, body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)
		require.Equal(t, http.StatusAccepted, rr.Code, "body: %s", rr.Body.String())
	})

	t.Run("deploy records the exposure and publishes nothing inline", func(t *testing.T) {
		deployAgent(t)

		row := exposure(t)
		require.NotNil(t, row, "the deploy recorded the exposure")
		assert.Equal(t, models.AgentExposureProtocolA2A, row.Protocol)
		assert.NotEqual(t, uuid.Nil, row.ArtifactUUID, "keyed on the artifact the gateway is told about")
		require.NotNil(t, row.AgentUUID)

		// The artifact the gateway is told about is the KindAgent row, which is
		// also what the agent's API keys are bound to.
		artifact, err := artifactRepo.GetByUUID(row.ArtifactUUID.String(), ouID)
		require.NoError(t, err)
		assert.Equal(t, models.KindAgent, artifact.Kind)

		var deployments int64
		require.NoError(t, gdb.Model(&models.Deployment{}).
			Where("artifact_uuid = ? AND ou_id = ?", row.ArtifactUUID, ouID).Count(&deployments).Error)
		assert.Zero(t, deployments, "the upstream is not readable until the release binding reconciles")
	})

	t.Run("redeploy keeps one exposure", func(t *testing.T) {
		before := exposure(t)
		require.NotNil(t, before)

		deployAgent(t)

		after := exposure(t)
		require.NotNil(t, after)
		assert.Equal(t, before.ID, after.ID)
		assert.Equal(t, before.ArtifactUUID, after.ArtifactUUID)
	})

	t.Run("deploy settings keep the exposure", func(t *testing.T) {
		before := exposure(t)
		require.NotNil(t, before)

		body := new(bytes.Buffer)
		require.NoError(t, json.NewEncoder(body).Encode(map[string]interface{}{
			"environmentName": before.EnvironmentName,
			"agentCardCorsConfig": map[string]interface{}{
				"enabled":      true,
				"allowOrigin":  []string{"*"},
				"allowHeaders": []string{"Content-Type"},
			},
		}))
		url := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s/deploy-settings",
			a2aLifecycleOrgName, a2aLifecycleProjName, a2aLifecycleAgentName)
		req := httptest.NewRequest(http.MethodPut, url, body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)
		require.Equal(t, http.StatusNoContent, rr.Code, "body: %s", rr.Body.String())

		after := exposure(t)
		require.NotNil(t, after)
		assert.Equal(t, before.ID, after.ID)
	})

	t.Run("delete removes the exposure", func(t *testing.T) {
		url := fmt.Sprintf("/api/v1/orgs/%s/projects/%s/agents/%s",
			a2aLifecycleOrgName, a2aLifecycleProjName, a2aLifecycleAgentName)
		req := httptest.NewRequest(http.MethodDelete, url, nil)
		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)
		require.Contains(t, []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent}, rr.Code,
			"body: %s", rr.Body.String())

		// Deletion is asynchronous in places, so give the cleanup a moment.
		require.Eventually(t, func() bool {
			return exposure(t) == nil
		}, 5*time.Second, 100*time.Millisecond,
			"a deleted agent leaves no exposure behind")
	})
}
