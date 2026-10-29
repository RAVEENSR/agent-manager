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

package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// seedAgentDeployment writes a gateway, an agent artifact and one deployment of it.
func seedAgentDeployment(t *testing.T) (artifactUUID uuid.UUID, ouID string) {
	t.Helper()
	gdb := db.GetDB()
	ouID = "ou-" + uuid.New().String()[:8]
	gatewayID, artifactUUID := uuid.New(), uuid.New()
	name := "dep-gw-" + gatewayID.String()[:8]
	require.NoError(t, gdb.Exec(
		`INSERT INTO gateways (uuid, name, display_name, vhost, gateway_functionality_type, ou_id, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)`,
		gatewayID, name, name, "gw.example", models.GatewayRoleIngress, ouID).Error)
	require.NoError(t, NewArtifactRepo(gdb).Create(gdb, &models.Artifact{
		UUID: artifactUUID, Handle: "proj/agent/" + artifactUUID.String(), Name: "agent-" + artifactUUID.String()[:8],
		Version: "v1.0", Kind: models.KindAgent, OUID: ouID,
	}))
	deployed := models.DeploymentStatusDeployed
	require.NoError(t, NewDeploymentRepo(gdb).CreateWithLimitEnforcement(&models.Deployment{
		Name: "agent-deployment", ArtifactUUID: artifactUUID, OUID: ouID, GatewayUUID: gatewayID,
		Content: []byte("kind: Agent\n"), Status: &deployed,
	}, 10))
	t.Cleanup(func() {
		gdb.Exec(`DELETE FROM artifacts WHERE uuid = ?`, artifactUUID)
		gdb.Exec(`DELETE FROM gateways WHERE uuid = ?`, gatewayID)
	})
	return artifactUUID, ouID
}

// A mode switch keeps the artifact (and its API keys) but no deployment of it may be served.
func TestDeploymentDeleteForArtifactInATransaction(t *testing.T) {
	repo := NewDeploymentRepo(db.GetDB())
	artifactUUID, ouID := seedAgentDeployment(t)
	gateways, err := repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	require.Len(t, gateways, 1)

	rolledBack := db.GetDB().Transaction(func(tx *gorm.DB) error {
		require.NoError(t, repo.WithTx(tx).DeleteForArtifact(context.Background(), artifactUUID, ouID))
		return errors.New("roll back")
	})
	require.Error(t, rolledBack)
	gateways, err = repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	assert.Len(t, gateways, 1, "a rolled-back delete leaves the deployment")

	require.NoError(t, repo.DeleteForArtifact(context.Background(), artifactUUID, ouID))
	gateways, err = repo.GetDeployedGatewaysByProvider(artifactUUID, ouID)
	require.NoError(t, err)
	assert.Empty(t, gateways, "deployment_status went with the deployments")
}

// Until the status core replaces them, the legacy writers must satisfy migration 051's CHECKs.
func TestDeploymentLegacyWritersRecordDesiredStateAndPerformedAt(t *testing.T) {
	artifactUUID, ouID := seedAgentDeployment(t)
	gdb := db.GetDB()
	var row models.DeploymentStatusRecord
	require.NoError(t, gdb.Where("artifact_uuid = ? AND ou_id = ?", artifactUUID, ouID).First(&row).Error)
	assert.Equal(t, models.DeploymentStatusDeployed, row.StatusDesired)
	assert.Equal(t, models.NewPerformedAt(row.PerformedAt), row.PerformedAt.UTC(), "performed_at is in milliseconds")
	require.NotNil(t, row.DeploymentID)

	_, err := NewDeploymentRepo(gdb).SetCurrentCtx(context.Background(), artifactUUID.String(), ouID,
		row.GatewayUUID.String(), row.DeploymentID.String(), models.DeploymentStatusUndeployed)
	require.NoError(t, err)
	var undeployed models.DeploymentStatusRecord
	require.NoError(t, gdb.Where("artifact_uuid = ? AND ou_id = ?", artifactUUID, ouID).First(&undeployed).Error)
	assert.Equal(t, models.DeploymentStatusUndeployed, undeployed.Status)
	assert.Equal(t, models.DeploymentStatusUndeployed, undeployed.StatusDesired)
	assert.False(t, undeployed.PerformedAt.Before(row.PerformedAt))

	_, err = NewDeploymentRepo(gdb).UpdateStatusByDeploymentID(row.DeploymentID.String(), row.GatewayUUID.String(),
		models.DeploymentStatusDeployed)
	require.NoError(t, err)
	var acked models.DeploymentStatusRecord
	require.NoError(t, gdb.Where("artifact_uuid = ? AND ou_id = ?", artifactUUID, ouID).First(&acked).Error)
	assert.Equal(t, models.DeploymentStatusDeployed, acked.StatusDesired)
}
