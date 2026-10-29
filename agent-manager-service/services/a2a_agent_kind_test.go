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
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/eventhub"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/reconcile"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
)

// a2aFixture is an org with an ingress gateway mapped to env, and the A2A kind over the real repositories with an
// OpenChoreo whose release binding serves url (empty: not yet).
type a2aFixture struct {
	sync      *syncFixture
	env       uuid.UUID
	gateway   uuid.UUID
	url       atomic.Value
	hub       *recordingEventHub
	core      *reconcile.Core
	worker    *reconcile.Worker
	publisher *A2APublisher
}

func newA2AFixture(t *testing.T) *a2aFixture {
	t.Helper()
	f := &a2aFixture{sync: newSyncFixture(t), env: uuid.New(), hub: &recordingEventHub{}}
	f.url.Store("")
	gormDB := db.GetDB()
	f.gateway = f.sync.seedGateway(t, f.sync.ouID)
	require.NoError(t, gormDB.Exec(`UPDATE gateways SET gateway_functionality_type = ? WHERE uuid = ?`,
		models.GatewayRoleIngress, f.gateway).Error)
	require.NoError(t, gormDB.Exec(`INSERT INTO gateway_environment_mappings (gateway_uuid, environment_uuid) VALUES (?, ?)`,
		f.gateway, f.env).Error)
	oc := &clientmocks.OpenChoreoClientMock{
		GetReleaseBindingServiceURLFunc: func(context.Context, string, string, string) (string, error) {
			return f.url.Load().(string), nil
		},
	}
	configs := &repomocks.AgentConfigRepositoryMock{
		GetFunc: func(context.Context, string, string, string, string) (*models.AgentConfig, error) {
			return &models.AgentConfig{}, nil
		},
	}
	tx := NewTxRunner(gormDB)
	exposures := repositories.NewPlatformAgentExposureRepository(gormDB)
	proxies := repositories.NewA2AProxyRepository(gormDB)
	cards := repositories.NewA2AAgentCardRepository(gormDB)
	gateways := repositories.NewGatewayRepo(gormDB)
	kind := NewA2AAgentKind(tx, exposures, proxies, cards, gateways, f.sync.repo, configs, oc, nil, nil, testLogger())
	f.core = reconcile.NewCore(gormDB, f.sync.repo, f.hub, reconcile.Kinds{kind},
		reconcile.Config{Lease: time.Minute, AttemptBudget: 3}, slog.New(slog.DiscardHandler))
	f.worker = reconcile.NewWorker(f.core, slog.New(slog.DiscardHandler))
	f.publisher = NewA2APublisher(tx, exposures, proxies, cards, repositories.NewArtifactRepo(gormDB), f.sync.repo,
		gateways, nil, testLogger(), f.core, kind)
	return f
}

// platformAgent is a platform agent's per-environment artifact and its publication key.
func (f *a2aFixture) platformAgent(t *testing.T) (models.ArtifactRef, a2aPublicationKey) {
	t.Helper()
	name := "agent-" + uuid.NewString()[:8]
	artifact, err := ensureAgentEnvAPIArtifact(db.GetDB(), repositories.NewArtifactRepo(db.GetDB()), f.sync.ouID, "proj",
		name, f.env.String())
	require.NoError(t, err)
	t.Cleanup(func() { db.GetDB().Exec(`DELETE FROM artifacts WHERE uuid = ?`, artifact.UUID) })
	agentUUID := uuid.New()
	return agentArtifactRef(artifact), a2aPublicationKey{
		OUID: f.sync.ouID, ProjectName: "proj", AgentName: name, EnvironmentName: "dev", EnvironmentUUID: f.env,
		AgentUUID: &agentUUID,
	}
}

func (f *a2aFixture) row(t *testing.T, artifact models.ArtifactRef) *models.DeploymentStatusRecord {
	t.Helper()
	row, err := f.sync.repo.GetStatusRecord(context.Background(), models.Placement{ArtifactRef: artifact, GatewayUUID: f.gateway})
	require.NoError(t, err)
	return row
}

// A platform agent deploys once its endpoint appears: PENDING while it waits, DEPLOYING when the deploy that
// makes the endpoint appear marks it due, DEPLOYED on the ack, which queues its card fetch with the agent's UUID.
func TestPlatformA2AAgentWaitsForItsEndpointThenGoesLive(t *testing.T) {
	f := newA2AFixture(t)
	artifact, key := f.platformAgent(t)
	publish := func() {
		require.NoError(t, f.publisher.tx(context.Background(), func(tx *gorm.DB) error {
			return f.publisher.Publish(context.Background(), tx, key, artifact)
		}))
		f.worker.RunOnce(context.Background())
	}

	publish()
	row := f.row(t, artifact)
	require.NotNil(t, row)
	assert.Equal(t, models.DeploymentStatusPending, row.Status)
	shown := models.UserStatusOf(*row)
	assert.Equal(t, models.DeploymentStatusDeploying, shown.Status)
	assert.Equal(t, waitingForEndpoint, shown.Reason)
	require.NotNil(t, row.NextAttemptAt)
	assert.True(t, row.NextAttemptAt.After(time.Now()), "a waiting row backs off")

	f.url.Store("http://agent.dev.svc:8080")
	publish()
	row = f.row(t, artifact)
	assert.Equal(t, models.DeploymentStatusDeploying, row.Status, "the deploy made the backed-off row due now")
	require.Len(t, f.hub.published, 1)
	assert.Equal(t, "agent.deployed", string(f.hub.published[0].EventType))

	applied, err := f.sync.repo.ApplyAck(context.Background(), f.gateway.String(), models.DeploymentAck{
		DeploymentID: row.DeploymentID.String(), ArtifactID: artifact.ArtifactUUID.String(), Action: "deploy",
		Status: "success", PerformedAt: row.PerformedAt,
	}, f.core.AfterLive())
	require.NoError(t, err)
	require.True(t, applied)
	assert.Equal(t, models.DeploymentStatusDeployed, f.row(t, artifact).Status)
	card, err := repositories.NewA2AAgentCardRepository(db.GetDB()).Get(context.Background(), key.OUID, key.ProjectName,
		key.AgentName, key.EnvironmentName)
	require.NoError(t, err)
	require.NotNil(t, card.AgentUUID)
	assert.Equal(t, *key.AgentUUID, *card.AgentUUID)
	t.Cleanup(func() { db.GetDB().Exec(`DELETE FROM a2a_agent_cards WHERE id = ?`, card.ID) })
}

// The placement pass places a platform agent from its exposure alone.
func TestPlacementPassPlacesAPlatformAgentFromItsExposure(t *testing.T) {
	f := newA2AFixture(t)
	artifact, key := f.platformAgent(t)
	require.NoError(t, f.publisher.Expose(context.Background(), nil, key, artifact.ArtifactUUID))
	f.url.Store("http://agent.dev.svc:8080")

	require.NoError(t, f.worker.PlaceEnv(context.Background(), f.sync.ouID, f.env))

	row := f.row(t, artifact)
	require.NotNil(t, row)
	assert.Equal(t, models.DeploymentStatusDeploying, row.Status)
}

// An agent delete drops each environment's artifact with its exposure and card rows and its delete events in one
// transaction: a failure part way leaves all of it for the retry.
func TestAgentDeleteIsAtomicPerEnvironment(t *testing.T) {
	f := newA2AFixture(t)
	artifact, key := f.platformAgent(t)
	f.url.Store("http://agent.dev.svc:8080")
	require.NoError(t, f.publisher.tx(context.Background(), func(tx *gorm.DB) error {
		return f.publisher.Publish(context.Background(), tx, key, artifact)
	}))
	f.worker.RunOnce(context.Background())
	require.Equal(t, models.DeploymentStatusDeploying, f.row(t, artifact).Status)
	cards := repositories.NewA2AAgentCardRepository(db.GetDB())
	require.NoError(t, cards.Enqueue(context.Background(), &models.A2AAgentCard{
		OUID: key.OUID, ProjectName: key.ProjectName, AgentName: key.AgentName, EnvironmentName: key.EnvironmentName,
		EnvironmentUUID: key.EnvironmentUUID, Source: models.A2AAgentCardSourcePlatform,
	}))
	t.Cleanup(func() { db.GetDB().Exec(`DELETE FROM a2a_agent_cards WHERE agent_name = ?`, key.AgentName) })
	svc := &agentManagerService{
		db: db.GetDB(), artifactRepo: repositories.NewArtifactRepo(db.GetDB()), a2aCardRepo: cards,
		a2aPublisher: f.publisher, logger: testLogger(),
	}
	exposures := repositories.NewPlatformAgentExposureRepository(db.GetDB())
	f.hub.fail = func(evt eventhub.Event) error {
		if evt.EventType == "agent.deleted" {
			return errors.New("event hub is down")
		}
		return nil
	}

	err := svc.deleteAgentAPIArtifact(context.Background(), key.OUID, key.ProjectName, key.AgentName, true)

	require.Error(t, err)
	_, err = repositories.NewArtifactRepo(db.GetDB()).GetByUUID(artifact.ArtifactUUID.String(), key.OUID)
	assert.NoError(t, err, "the artifact stays")
	_, err = exposures.GetByArtifact(context.Background(), key.OUID, artifact.ArtifactUUID)
	assert.NoError(t, err, "the exposure stays")
	_, err = cards.Get(context.Background(), key.OUID, key.ProjectName, key.AgentName, key.EnvironmentName)
	assert.NoError(t, err, "the card stays")
	assert.NotNil(t, f.row(t, artifact), "the status row stays")

	f.hub.fail = nil
	require.NoError(t, svc.deleteAgentAPIArtifact(context.Background(), key.OUID, key.ProjectName, key.AgentName, true))

	_, err = exposures.GetByArtifact(context.Background(), key.OUID, artifact.ArtifactUUID)
	assert.Error(t, err, "the exposure is gone")
	_, err = cards.Get(context.Background(), key.OUID, key.ProjectName, key.AgentName, key.EnvironmentName)
	assert.Error(t, err, "the card is gone")
	assert.Nil(t, f.row(t, artifact))
	last := f.hub.published[len(f.hub.published)-1]
	assert.Equal(t, "agent.deleted", string(last.EventType))
}
