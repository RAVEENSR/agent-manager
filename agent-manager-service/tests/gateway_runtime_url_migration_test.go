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
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/db"
	dbmigrations "github.com/wso2/agent-manager/agent-manager-service/db_migrations"
)

// runtimeURLTestTx opens a transaction rolled back at test end, so seeded rows and
// the NULLed runtime_url never persist for another test to observe.
func runtimeURLTestTx(t *testing.T) *gorm.DB {
	t.Helper()
	tx := db.DB(context.Background()).Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { require.NoError(t, tx.Rollback().Error) })
	return tx
}

// seedRuntimeURLGateway inserts a gateway with an explicit name and a NULL runtime_url,
// i.e. the pre-038 state, and returns its UUID.
func seedRuntimeURLGateway(t *testing.T, tx *gorm.DB, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, tx.Exec(
		`INSERT INTO gateways (uuid, ou_id, name, display_name, description, properties,
		                       vhost, is_critical, gateway_functionality_type, is_active,
		                       runtime_url, created_at, updated_at)
		 VALUES (?, 'runtime-url-test-org', ?, ?, '', '{}'::jsonb,
		         'https://ext.example.com', false, 'both', false,
		         NULL, now(), now())`,
		id, name, name,
	).Error)
	return id
}

// runtimeURLOf returns the raw column value. sql.NullString, not *string: the NULL vs
// populated distinction is the whole point of these assertions.
func runtimeURLOf(t *testing.T, tx *gorm.DB, id uuid.UUID) sql.NullString {
	t.Helper()
	var got sql.NullString
	require.NoError(t, tx.Raw(`SELECT runtime_url FROM gateways WHERE uuid = ?`, id).Row().Scan(&got))
	return got
}

func TestMigration038BackfillsConformingName(t *testing.T) {
	tx := runtimeURLTestTx(t)
	id := seedRuntimeURLGateway(t, tx, "api-platform-acme-dev")

	require.NoError(t, dbmigrations.BackfillGatewayRuntimeURL(tx))

	got := runtimeURLOf(t, tx, id)
	require.True(t, got.Valid)
	require.Equal(t, "http://api-platform-acme-dev-gw-gateway-gateway-runtime.acme-dev:22893", got.String)
}

func TestMigration038LeavesNonConformingNameNull(t *testing.T) {
	tx := runtimeURLTestTx(t)
	// "default" lacks the api-platform- prefix, so the pre-038 derivation returned ""
	// for it — this is the `make setup-ai-gateway` row. NULL preserves that.
	id := seedRuntimeURLGateway(t, tx, "default")

	require.NoError(t, dbmigrations.BackfillGatewayRuntimeURL(tx))

	require.False(t, runtimeURLOf(t, tx, id).Valid)
}

func TestMigration038LeavesBareVanityPrefixNull(t *testing.T) {
	tx := runtimeURLTestTx(t)
	// name == prefix derives an empty namespace, which the pre-038 code returned "" for.
	id := seedRuntimeURLGateway(t, tx, "api-platform-")

	require.NoError(t, dbmigrations.BackfillGatewayRuntimeURL(tx))

	require.False(t, runtimeURLOf(t, tx, id).Valid)
}

func TestMigration038HonoursOverriddenEnvVars(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME_NAME_PREFIX", "custom-")
	t.Setenv("GATEWAY_RUNTIME_SERVICE_SUFFIX", "-runtime")
	t.Setenv("GATEWAY_RUNTIME_PORT", "9443")

	tx := runtimeURLTestTx(t)
	id := seedRuntimeURLGateway(t, tx, "custom-acme-dev")

	require.NoError(t, dbmigrations.BackfillGatewayRuntimeURL(tx))

	got := runtimeURLOf(t, tx, id)
	require.True(t, got.Valid)
	require.Equal(t, "http://custom-acme-dev-runtime.acme-dev:9443", got.String)
}
