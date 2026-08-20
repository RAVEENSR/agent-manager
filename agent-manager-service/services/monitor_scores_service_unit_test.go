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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// publishScoresRequest is a minimal valid body.
func publishScoresRequest() *models.PublishScoresRequest {
	started := time.Now()
	return &models.PublishScoresRequest{
		AggregatedScores: []models.PublishAggregateItem{{
			Identifier:    "toxicity",
			EvaluatorName: "toxicity",
			Level:         "trace",
			Aggregations:  map[string]interface{}{"mean": 0.0},
			Count:         1,
		}},
		IndividualScores: []models.PublishScoreItem{{
			TraceID:        "trace-1",
			EvaluatorName:  "toxicity",
			Level:          "trace",
			TraceStartTime: &started,
		}},
	}
}

func newScoresServiceForOwnership(t *testing.T, monitorRepo repositories.MonitorRepository) *MonitorScoresService {
	t.Helper()
	scoreRepo := &repomocks.ScoreRepositoryMock{
		RunInTransactionFunc: func(_ func(repositories.ScoreRepository) error) error {
			t.Fatal("score repository must not be reached for a rejected publish")
			return nil
		},
	}
	return NewMonitorScoresService(scoreRepo, monitorRepo, discardLogger())
}

func TestPublishScoresRejectsMonitorOwnedByAnotherOrg(t *testing.T) {
	monitorID, runID := uuid.New(), uuid.New()
	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByIDFunc: func(id uuid.UUID) (*models.Monitor, error) {
			assert.Equal(t, monitorID, id)
			return &models.Monitor{ID: id, OUID: "victim-org"}, nil
		},
	}

	err := newScoresServiceForOwnership(t, monitorRepo).
		PublishScores(monitorID, runID, "attacker-org", publishScoresRequest())

	require.Error(t, err)
	assert.ErrorIs(t, err, utils.ErrForbidden)
}

func TestPublishScoresRejectsRunBelongingToAnotherMonitor(t *testing.T) {
	monitorID, runID := uuid.New(), uuid.New()
	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByIDFunc: func(id uuid.UUID) (*models.Monitor, error) {
			return &models.Monitor{ID: id, OUID: "acme"}, nil
		},
		GetMonitorRunByIDFunc: func(gotRun, gotMonitor uuid.UUID) (*models.MonitorRun, error) {
			assert.Equal(t, runID, gotRun)
			assert.Equal(t, monitorID, gotMonitor)
			return nil, gorm.ErrRecordNotFound
		},
	}

	err := newScoresServiceForOwnership(t, monitorRepo).
		PublishScores(monitorID, runID, "acme", publishScoresRequest())

	require.Error(t, err)
	assert.ErrorIs(t, err, utils.ErrNotFound)
}

func TestPublishScoresRejectsUnknownMonitor(t *testing.T) {
	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByIDFunc: func(_ uuid.UUID) (*models.Monitor, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}

	err := newScoresServiceForOwnership(t, monitorRepo).
		PublishScores(uuid.New(), uuid.New(), "acme", publishScoresRequest())

	require.Error(t, err)
	assert.ErrorIs(t, err, utils.ErrNotFound)
}

// An empty caller org must be rejected, not treated as matching every monitor.
func TestPublishScoresRejectsEmptyCallerOrg(t *testing.T) {
	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByIDFunc: func(id uuid.UUID) (*models.Monitor, error) {
			return &models.Monitor{ID: id, OUID: "acme"}, nil
		},
	}

	err := newScoresServiceForOwnership(t, monitorRepo).
		PublishScores(uuid.New(), uuid.New(), "", publishScoresRequest())

	require.Error(t, err)
	assert.ErrorIs(t, err, utils.ErrForbidden)
}

// A lookup failure must not be mistaken for a passed ownership check.
func TestPublishScoresPropagatesMonitorLookupFailure(t *testing.T) {
	dbErr := gorm.ErrInvalidDB
	monitorRepo := &repomocks.MonitorRepositoryMock{
		GetMonitorByIDFunc: func(_ uuid.UUID) (*models.Monitor, error) {
			return nil, dbErr
		},
	}

	err := newScoresServiceForOwnership(t, monitorRepo).
		PublishScores(uuid.New(), uuid.New(), "acme", publishScoresRequest())

	require.Error(t, err)
	assert.ErrorIs(t, err, dbErr)
	assert.NotErrorIs(t, err, utils.ErrNotFound)
	assert.NotErrorIs(t, err, utils.ErrForbidden)
}
