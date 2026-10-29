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
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// proxyFixture is an org with a provider and the LLM proxy services over the real repositories.
type proxyFixture struct {
	*providerFixture
	provider *models.LLMProvider
	kind     *LLMProxyKind
	worker   *reconcile.Worker
	proxies  *LLMProxyDeploymentService
}

func newProxyFixture(t *testing.T) *proxyFixture {
	t.Helper()
	f := &proxyFixture{providerFixture: newProviderFixture(t)}
	gormDB := db.GetDB()
	gateways := repositories.NewGatewayRepo(gormDB)
	f.kind = NewLLMProxyKind(gateways, f.sync.repo)
	core := reconcile.NewCore(gormDB, f.sync.repo, f.hub, reconcile.Kinds{NewLLMProviderKind(), f.kind},
		reconcile.Config{Lease: time.Minute, AttemptBudget: 3}, slog.New(slog.DiscardHandler))
	f.worker = reconcile.NewWorker(core, slog.New(slog.DiscardHandler))
	f.proxies = NewLLMProxyDeploymentService(NewTxRunner(gormDB), f.sync.repo, repositories.NewLLMProxyRepo(gormDB),
		repositories.NewLLMProviderRepo(gormDB), gateways, nil, core, f.kind)
	created, err := f.providers.Create(context.Background(), f.sync.ouID, "me", f.newProvider(t))
	require.NoError(t, err)
	f.provider = created
	return f
}

// mapGateway adds an egress gateway mapped to env.
func (f *proxyFixture) mapGateway(t *testing.T, env uuid.UUID) uuid.UUID {
	t.Helper()
	gateway := f.sync.seedGateway(t, f.sync.ouID)
	require.NoError(t, db.GetDB().Exec(
		`INSERT INTO gateway_environment_mappings (gateway_uuid, environment_uuid) VALUES (?, ?)`, gateway, env).Error)
	return gateway
}

func (f *proxyFixture) newProxy(t *testing.T) *models.LLMProxy {
	t.Helper()
	proxy := goldenProxy()
	proxy.UUID = uuid.Nil
	proxy.Handle = "px-" + uuid.NewString()[:8]
	proxy.ProjectUUID = uuid.New()
	proxy.ProviderUUID = f.provider.UUID
	proxy.Configuration.Provider = f.provider.UUID.String()
	require.NoError(t, repositories.NewLLMProxyRepo(db.GetDB()).Create(context.Background(), proxy, proxy.Handle,
		proxy.Handle, "v1.0", f.sync.ouID))
	t.Cleanup(func() { db.GetDB().Exec(`DELETE FROM artifacts WHERE uuid = ?`, proxy.UUID) })
	return proxy
}

// mapToAgent saves the agent-config record that says proxy should be deployed in env.
func (f *proxyFixture) mapToAgent(t *testing.T, proxy *models.LLMProxy, env uuid.UUID) {
	t.Helper()
	configUUID := uuid.New()
	require.NoError(t, db.GetDB().Exec(
		`INSERT INTO agent_configurations (uuid, name, agent_id, project_name, ou_id) VALUES (?, ?, ?, 'p', ?)`,
		configUUID, "c-"+configUUID.String()[:8], "a-"+configUUID.String()[:8], f.sync.ouID).Error)
	require.NoError(t, db.GetDB().Exec(
		`INSERT INTO env_agent_model_mapping (config_uuid, environment_uuid, llm_proxy_uuid) VALUES (?, ?, ?)`,
		configUUID, env, proxy.UUID).Error)
	t.Cleanup(func() { db.GetDB().Exec(`DELETE FROM agent_configurations WHERE uuid = ?`, configUUID) })
}

func (f *proxyFixture) row(t *testing.T, proxy *models.LLMProxy, gateway uuid.UUID) *models.DeploymentStatusRecord {
	t.Helper()
	row, err := f.sync.repo.GetStatusRecord(context.Background(), models.Placement{
		ArtifactRef: models.ArtifactRef{OUID: f.sync.ouID, ArtifactUUID: proxy.UUID}, GatewayUUID: gateway,
	})
	require.NoError(t, err)
	return row
}

// An agent-config proxy undeployed from one gateway through the public route keeps its other placement.
func TestAgentConfigProxyUndeployedOnOneGatewayKeepsTheOther(t *testing.T) {
	f := newProxyFixture(t)
	proxy := f.newProxy(t)
	envA, envB := uuid.New(), uuid.New()
	gatewayA, gatewayB := f.mapGateway(t, envA), f.mapGateway(t, envB)
	f.mapToAgent(t, proxy, envA)
	f.mapToAgent(t, proxy, envB)
	deployedA, err := f.proxies.DeployForEnvironment(context.Background(), proxy, f.sync.ouID, envA, gatewayA)
	require.NoError(t, err)
	_, err = f.proxies.DeployForEnvironment(context.Background(), proxy, f.sync.ouID, envB, gatewayB)
	require.NoError(t, err)

	_, err = f.proxies.UndeployLLMProxyDeployment(context.Background(), proxy.Handle,
		deployedA.DeploymentID.String(), gatewayA.String(), f.sync.ouID)
	require.NoError(t, err)

	assert.Equal(t, models.DeploymentStatusUndeploying, f.row(t, proxy, gatewayA).Status)
	assert.Equal(t, models.DeploymentStatusDeploying, f.row(t, proxy, gatewayB).Status)
	require.NoError(t, f.worker.PlaceEnv(context.Background(), f.sync.ouID, envA))
	assert.Equal(t, models.DeploymentStatusUndeployed, f.row(t, proxy, gatewayA).StatusDesired,
		"the placement pass doesn't re-place an undeployed proxy whose record remains")
}

// An agent's proxy whose security config can't render is refused before anything is written, whether or not its
// environment has a gateway.
func TestAgentConfigProxyWithAnInvalidSecurityConfigCommitsNothing(t *testing.T) {
	f := newProxyFixture(t)
	proxy := f.newProxy(t)
	proxy.Configuration.Security.APIKey.Key = ""
	require.NoError(t, repositories.NewLLMProxyRepo(db.GetDB()).Update(proxy, proxy.Handle, f.sync.ouID))

	t.Run("with a gateway", func(t *testing.T) {
		env := uuid.New()
		gateway := f.mapGateway(t, env)
		_, err := f.proxies.DeployForEnvironment(context.Background(), proxy, f.sync.ouID, env, gateway)
		assert.ErrorIs(t, err, utils.ErrInvalidInput)
		assert.Nil(t, f.row(t, proxy, gateway))
	})
	t.Run("without a gateway", func(t *testing.T) {
		record := reconcile.Record{
			ArtifactRef:     models.ArtifactRef{OUID: f.sync.ouID, ArtifactUUID: proxy.UUID, Kind: models.KindLLMProxy, Handle: proxy.Handle},
			EnvironmentUUID: uuid.New(),
		}
		err := NewTxRunner(db.GetDB())(context.Background(), func(tx *gorm.DB) error {
			_, err := f.proxies.core.DeployRecord(context.Background(), tx, f.kind, record)
			return err
		})
		assert.ErrorIs(t, err, utils.ErrInvalidInput)
	})
	assert.Empty(t, f.hub.published)
}

// A provider auth-header change re-renders each dependent proxy where it is placed, and nothing else.
func TestRedeployEverywhereRendersOnlyPlacedRows(t *testing.T) {
	f := newProxyFixture(t)
	proxy, other := f.newProxy(t), f.newProxy(t)
	envA, envB := uuid.New(), uuid.New()
	gatewayA, gatewayB := f.mapGateway(t, envA), f.mapGateway(t, envB)
	deployedA, err := f.proxies.DeployForEnvironment(context.Background(), proxy, f.sync.ouID, envA, gatewayA)
	require.NoError(t, err)
	deployedB, err := f.proxies.DeployForEnvironment(context.Background(), proxy, f.sync.ouID, envB, gatewayB)
	require.NoError(t, err)
	otherDeployed, err := f.proxies.DeployForEnvironment(context.Background(), other, f.sync.ouID, envA, gatewayA)
	require.NoError(t, err)
	_, err = f.proxies.UndeployLLMProxyDeployment(context.Background(), proxy.Handle,
		deployedB.DeploymentID.String(), gatewayB.String(), f.sync.ouID)
	require.NoError(t, err)

	require.NoError(t, f.proxies.RedeployEverywhere(context.Background(), proxy, f.sync.ouID, "provider changed"))

	rowA := f.row(t, proxy, gatewayA)
	assert.NotEqual(t, deployedA.DeploymentID, *rowA.DeploymentID, "the placed row re-rendered")
	assert.Equal(t, models.DeploymentStatusDeploying, rowA.Status)
	assert.Equal(t, models.DeploymentStatusUndeploying, f.row(t, proxy, gatewayB).Status, "an undeployed row stays")
	assert.Equal(t, otherDeployed.DeploymentID, *f.row(t, other, gatewayA).DeploymentID, "another proxy is untouched")
}

// Tearing an agent's or monitor's proxy down undeploys every gateway it is placed on, whatever the row's status:
// a deploy the gateway hasn't acked yet is undeployed too.
func TestUndeployEverywhereUndeploysEveryPlacedRow(t *testing.T) {
	f := newProxyFixture(t)
	proxy := f.newProxy(t)
	envA, envB := uuid.New(), uuid.New()
	gatewayA, gatewayB := f.mapGateway(t, envA), f.mapGateway(t, envB)
	_, err := f.proxies.DeployForEnvironment(context.Background(), proxy, f.sync.ouID, envA, gatewayA)
	require.NoError(t, err)
	_, err = f.proxies.DeployForEnvironment(context.Background(), proxy, f.sync.ouID, envB, gatewayB)
	require.NoError(t, err)
	require.Equal(t, models.DeploymentStatusDeploying, f.row(t, proxy, gatewayA).Status)

	require.NoError(t, f.proxies.UndeployEverywhere(context.Background(), proxy.Handle, f.sync.ouID))

	for _, gateway := range []uuid.UUID{gatewayA, gatewayB} {
		row := f.row(t, proxy, gateway)
		assert.Equal(t, models.DeploymentStatusUndeploying, row.Status)
		assert.Equal(t, models.DeploymentStatusUndeployed, row.StatusDesired)
	}
	assert.ErrorIs(t, f.proxies.UndeployEverywhere(context.Background(), "no-such-proxy", f.sync.ouID),
		utils.ErrLLMProxyNotFound)
}

// The placement pass places an agent's proxy from its record alone, on its environment's egress gateway.
func TestPlacementPassPlacesAnAgentConfigProxy(t *testing.T) {
	f := newProxyFixture(t)
	proxy := f.newProxy(t)
	env := uuid.New()
	f.mapToAgent(t, proxy, env)
	gateway := f.mapGateway(t, env)

	require.NoError(t, f.worker.PlaceEnv(context.Background(), f.sync.ouID, env))

	row := f.row(t, proxy, gateway)
	require.NotNil(t, row)
	assert.Equal(t, models.DeploymentStatusDeploying, row.Status)
}

// An agent-config save after the proxy was undeployed from its environment's gateway through the public route has
// nothing placed to refresh there: it saves.
func TestAgentConfigProxyUpdateAfterAPublicUndeploySaves(t *testing.T) {
	f := newProxyFixture(t)
	proxy := f.newProxy(t)
	env := uuid.New()
	gateway := f.mapGateway(t, env)
	f.mapToAgent(t, proxy, env)
	deployed, err := f.proxies.DeployForEnvironment(context.Background(), proxy, f.sync.ouID, env, gateway)
	require.NoError(t, err)
	_, err = f.proxies.UndeployLLMProxyDeployment(context.Background(), proxy.Handle,
		deployed.DeploymentID.String(), gateway.String(), f.sync.ouID)
	require.NoError(t, err)

	gormDB := db.GetDB()
	mappings := repositories.NewEnvAgentModelMappingRepository(gormDB)
	var mapping models.EnvAgentModelMapping
	require.NoError(t, gormDB.Preload("LLMProxy").Where("llm_proxy_uuid = ?", proxy.UUID).First(&mapping).Error)
	mapping.LLMProxy.Handle = proxy.Handle
	svc := &agentConfigurationService{
		db:                        gormDB,
		envMappingRepo:            mappings,
		llmProviderRepo:           repositories.NewLLMProviderRepo(gormDB),
		gatewayRepo:               repositories.NewGatewayRepo(gormDB),
		llmProxyService:           NewLLMProxyService(repositories.NewLLMProxyRepo(gormDB), repositories.NewLLMProviderRepo(gormDB), make([]byte, 32)),
		llmProxyDeploymentService: f.proxies,
		logger:                    slog.New(slog.DiscardHandler),
	}
	config := &models.AgentConfiguration{OUID: f.sync.ouID, AgentID: "a"}
	request := models.EnvModelConfigRequest{ProviderName: f.provider.Configuration.Handle}

	rollback, err := svc.processEnvProxyUpdate(context.Background(), config, nil, env, "dev", request, &mapping, f.sync.ouID)

	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, rollback.deploymentID)
	assert.Equal(t, models.DeploymentStatusUndeployed, f.row(t, proxy, gateway).StatusDesired, "nothing is re-placed")
}

// A public deploy is refused on a second gateway of an environment the artifact is already placed in, before the
// first placement's ack.
func TestPublicDeployRefusesASecondGatewayOfAPlacedEnvironment(t *testing.T) {
	f := newProxyFixture(t)
	env := uuid.New()
	first, second := f.mapGateway(t, env), f.mapGateway(t, env)

	t.Run("LLM proxy", func(t *testing.T) {
		proxy := f.newProxy(t)
		_, err := f.proxies.DeployLLMProxy(proxy.Handle, &models.DeployAPIRequest{GatewayID: first.String()}, f.sync.ouID)
		require.NoError(t, err)
		require.Equal(t, models.DeploymentStatusDeploying, f.row(t, proxy, first).Status)

		_, err = f.proxies.DeployLLMProxy(proxy.Handle, &models.DeployAPIRequest{GatewayID: second.String()}, f.sync.ouID)

		assert.ErrorIs(t, err, utils.ErrInvalidInput)
	})
	t.Run("LLM provider", func(t *testing.T) {
		_, err := f.deployment.DeployLLMProvider(f.provider.UUID.String(),
			&models.DeployAPIRequest{GatewayID: first.String()}, f.sync.ouID)
		require.NoError(t, err)

		_, err = f.deployment.DeployLLMProvider(f.provider.UUID.String(),
			&models.DeployAPIRequest{GatewayID: second.String()}, f.sync.ouID)

		assert.ErrorIs(t, err, utils.ErrInvalidInput)
	})
}

// A proxy's deploy event reads the vhost in the deploying transaction, so a vhost saved in it reaches the event.
func TestLLMProxyEventReadsTheVhostInTheDeployingTransaction(t *testing.T) {
	f := newProxyFixture(t)
	proxy := f.newProxy(t)
	env := uuid.New()
	gateway := f.mapGateway(t, env)
	vhost := "saved-" + uuid.NewString()[:8] + ".example.com"

	err := NewTxRunner(db.GetDB())(context.Background(), func(tx *gorm.DB) error {
		proxy.Configuration.Vhost = &vhost
		if err := repositories.NewLLMProxyRepo(tx).Update(proxy, proxy.Handle, f.sync.ouID); err != nil {
			return err
		}
		_, err := f.proxies.core.Deploy(context.Background(), tx, proxyPlacement(proxy, proxy.Handle, f.sync.ouID, gateway))
		return err
	})

	require.NoError(t, err)
	require.NotEmpty(t, f.hub.published)
	assert.Contains(t, f.hub.published[len(f.hub.published)-1].EventData, vhost)
}
