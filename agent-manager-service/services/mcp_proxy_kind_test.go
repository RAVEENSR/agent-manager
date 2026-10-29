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

package services

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// mcpFixture is an org with an MCP proxy whose one endpoint is bound to env, and the MCP service over the real
// repositories.
type mcpFixture struct {
	*proxyFixture
	svc      *MCPProxyService
	proxy    *models.MCPProxy
	env      uuid.UUID
	artifact uuid.UUID
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	f := &mcpFixture{proxyFixture: newProxyFixture(t)}
	gormDB := db.GetDB()
	gateways := repositories.NewGatewayRepo(gormDB)
	kind := NewMCPProxyKind(gateways, f.sync.repo)
	core := reconcile.NewCore(gormDB, f.sync.repo, f.hub, reconcile.Kinds{kind},
		reconcile.Config{Lease: time.Minute, AttemptBudget: 3}, slog.New(slog.DiscardHandler))
	f.worker = reconcile.NewWorker(core, slog.New(slog.DiscardHandler))
	f.svc = &MCPProxyService{
		db: gormDB, tx: NewTxRunner(gormDB), repo: repositories.NewMCPProxyRepo(gormDB), deploymentRepo: f.sync.repo,
		gatewayRepo: gateways, artifactRepo: repositories.NewArtifactRepo(gormDB),
		mcpProxyScopeRepo: repositories.NewMCPProxyScopeRepository(gormDB), logger: slog.New(slog.DiscardHandler),
		core: core, kind: kind,
	}

	proxy, _ := goldenMCPProxy()
	proxy.UUID = uuid.New()
	proxy.Handle = "mcp-" + proxy.UUID.String()[:8]
	f.env, f.artifact = uuid.New(), uuid.New()
	proxy.Endpoints[0].UUID = uuid.New()
	proxy.Endpoints[0].Environments[0].EnvironmentUUID = f.env
	proxy.Endpoints[0].Environments[0].ArtifactUUID = f.artifact
	proxy.Endpoints[0].Environments[0].MCPProxyUUID = proxy.UUID
	require.NoError(t, gormDB.Transaction(func(tx *gorm.DB) error {
		handle := mcpProxyEnvArtifactHandle(proxy.Handle, "primary", f.env.String())
		if err := repositories.NewArtifactRepo(tx).Create(tx, &models.Artifact{
			UUID: f.artifact, Handle: handle, Name: handle, Version: "v1.0", Kind: models.KindMCPMapping, OUID: f.sync.ouID,
		}); err != nil {
			return err
		}
		return repositories.NewMCPProxyRepo(gormDB).Create(context.Background(), tx, proxy, proxy.Handle, proxy.Handle,
			"v1.0", f.sync.ouID)
	}))
	t.Cleanup(func() {
		gormDB.Exec(`DELETE FROM artifacts WHERE uuid IN (?, ?)`, proxy.UUID, f.artifact)
	})
	loaded, err := f.svc.repo.GetByUUID(context.Background(), proxy.UUID.String(), f.sync.ouID)
	require.NoError(t, err)
	f.proxy = loaded
	return f
}

func (f *mcpFixture) mcpRow(t *testing.T, gateway uuid.UUID) *models.DeploymentStatusRecord {
	t.Helper()
	return f.row(t, &models.LLMProxy{UUID: f.artifact}, gateway)
}

// A request's explicit gateway is the row's gateway, even with a second egress gateway in the environment.
func TestMCPRequestedGatewayBecomesTheRowsGateway(t *testing.T) {
	f := newMCPFixture(t)
	f.mapGateway(t, f.env)
	requested := f.mapGateway(t, f.env)
	requestedID := requested.String()
	f.proxy.Endpoints[0].Environments[0].RequestedGatewayUUID = &requestedID

	require.NoError(t, f.svc.deployMCPProxyEndpoints(context.Background(), f.proxy, f.sync.ouID))

	row := f.mcpRow(t, requested)
	require.NotNil(t, row)
	assert.Equal(t, models.DeploymentStatusDeploying, row.Status)
	placed, err := f.sync.repo.PlacedGateways(context.Background(), f.sync.ouID, f.artifact)
	require.NoError(t, err)
	assert.Equal(t, []string{requestedID}, placed)
}

// A scope change re-renders the proxy's placed artifacts before the request returns.
func TestMCPScopeChangeReRendersInline(t *testing.T) {
	f := newMCPFixture(t)
	gateway := f.mapGateway(t, f.env)
	require.NoError(t, f.svc.deployMCPProxyEndpoints(context.Background(), f.proxy, f.sync.ouID))
	before := *f.mcpRow(t, gateway).DeploymentID
	require.NoError(t, db.GetDB().Create(&models.MCPProxyScope{
		MCPProxyUUID: f.proxy.UUID, Action: "read", Tools: []string{"get_forecast"},
	}).Error)
	enabled := true
	f.proxy.Endpoints[0].Configuration.Security = &models.SecurityConfig{Enabled: &enabled,
		Identity: &models.IdentitySecurity{Enabled: &enabled}}
	require.NoError(t, db.GetDB().Model(&models.MCPProxyEndpoint{}).Where("uuid = ?", f.proxy.Endpoints[0].UUID).
		Update("configuration", f.proxy.Endpoints[0].Configuration).Error)

	require.NoError(t, f.svc.RedeployMCPProxy(context.Background(), f.proxy, f.sync.ouID))

	row := f.mcpRow(t, gateway)
	assert.NotEqual(t, before, *row.DeploymentID, "the scope change rendered a new deployment")
	assert.Nil(t, row.NextAttemptAt, "rendered inline, nothing left owed")
	pulled, err := f.sync.repo.GetPullable(context.Background(), f.sync.ouID, f.artifact, gateway.String())
	require.NoError(t, err)
	assert.Contains(t, string(pulled.Content), "get_forecast")
}

// The placement pass places a binding whose environment gains a gateway, and a deleted binding's artifact sends its
// delete event in the deleting transaction.
func TestMCPPlacementAndDelete(t *testing.T) {
	f := newMCPFixture(t)
	require.NoError(t, f.svc.deployMCPProxyEndpoints(context.Background(), f.proxy, f.sync.ouID))
	gateway := f.mapGateway(t, f.env)
	assert.Nil(t, f.mcpRow(t, gateway), "no gateway, no row")

	require.NoError(t, f.worker.PlaceEnv(context.Background(), f.sync.ouID, f.env))
	require.NotNil(t, f.mcpRow(t, gateway))
	assert.Equal(t, models.DeploymentStatusDeploying, f.mcpRow(t, gateway).Status)

	require.NoError(t, f.svc.deleteMCPProxyEnvironmentArtifacts(context.Background(), []uuid.UUID{f.artifact}, f.sync.ouID))

	last := f.hub.published[len(f.hub.published)-1]
	assert.Equal(t, "mcpproxy.deleted", string(last.EventType))
	assert.Equal(t, gateway.String(), last.GatewayID)
	assert.Nil(t, f.mcpRow(t, gateway))
}
