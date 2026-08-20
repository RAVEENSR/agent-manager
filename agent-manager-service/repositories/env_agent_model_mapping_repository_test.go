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
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// TestEnvAgentModelMappingRepo_BackfillsLLMProxyHandle guards against a
// regression where deleting an agent's LLM model config failed with
// "failed to get deployments for proxy \"\": failed to get proxy: record
// not found". LLMProxy.Handle is gorm:"-" (derived, not a DB column), so
// GORM's Preload("LLMProxy") always leaves it blank; the delete flow reads
// mapping.LLMProxy.Handle straight off the ListByConfig/GetByConfigAndEnv
// result to look up proxy deployments to clean up. Without backfilling it
// from Configuration.Name (as agent_configuration_repository.go's
// GetByUUID/GetByAgentID already do via backfillLLMProxyHandles), the
// lookup always resolves an empty handle and the whole delete aborts.
func TestEnvAgentModelMappingRepo_BackfillsLLMProxyHandle(t *testing.T) {
	gdb := db.GetDB()
	ctx := context.Background()

	config := &models.AgentConfiguration{
		Name:        "backfill-handle-agent-config",
		AgentID:     "backfill-handle-agent",
		OUID:        "test-ou-backfill-handle",
		ProjectName: "test-proj-backfill-handle",
	}
	require.NoError(t, gdb.Create(config).Error)

	// Seed an llm_proxies row directly, same as monitor_executor_test.go's
	// TestExecuteMonitorRun_LLMCredentials: disable FK triggers so we don't
	// need to also seed artifacts/llm_providers rows just to get a proxy with
	// a Configuration.Name in place.
	const proxyName = "backfill-handle-proxy-name"
	proxyUUID := uuid.New()
	require.NoError(t, gdb.Connection(func(tx *gorm.DB) error {
		if err := tx.Exec("SET session_replication_role = 'replica'").Error; err != nil {
			return err
		}
		// Restore on every exit path (including an INSERT failure) so a pooled
		// connection is never handed back to another test stuck in 'replica'
		// mode, which would silently skip FK/trigger checks for it too.
		defer func() {
			_ = tx.Exec("SET session_replication_role = 'origin'").Error
		}()
		return tx.Exec(
			`INSERT INTO llm_proxies (uuid, project_uuid, provider_uuid, status, configuration)
			 VALUES (?, ?, ?, 'deployed', ?)`,
			proxyUUID, uuid.New(), uuid.New(), `{"name":"`+proxyName+`"}`,
		).Error
	}))

	envUUID := uuid.New()
	mapping := &models.EnvAgentModelMapping{
		ConfigUUID:      config.UUID,
		EnvironmentUUID: envUUID,
		LLMProxyUUID:    proxyUUID,
	}
	require.NoError(t, gdb.Create(mapping).Error)

	t.Cleanup(func() {
		gdb.Where("config_uuid = ?", config.UUID).Delete(&models.EnvAgentModelMapping{})
		gdb.Exec("DELETE FROM llm_proxies WHERE uuid = ?", proxyUUID)
		gdb.Delete(config)
	})

	repo := NewEnvAgentModelMappingRepository(gdb)

	t.Run("ListByConfig backfills Handle", func(t *testing.T) {
		mappings, err := repo.ListByConfig(ctx, config.UUID)
		require.NoError(t, err)
		require.Len(t, mappings, 1)
		require.NotNil(t, mappings[0].LLMProxy)
		require.Equal(t, proxyName, mappings[0].LLMProxy.Handle,
			"ListByConfig must backfill LLMProxy.Handle so delete cleanup can resolve the proxy")
	})

	t.Run("GetByConfigAndEnv backfills Handle", func(t *testing.T) {
		got, err := repo.GetByConfigAndEnv(ctx, config.UUID, envUUID)
		require.NoError(t, err)
		require.NotNil(t, got.LLMProxy)
		require.Equal(t, proxyName, got.LLMProxy.Handle,
			"GetByConfigAndEnv must backfill LLMProxy.Handle so delete cleanup can resolve the proxy")
	})
}
