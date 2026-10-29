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

package eventhub

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/db"
)

// Every event takes the next number in its gateway's sequence, under the gateway-state row lock.
func TestPublishEventNumbersEventsPerGateway(t *testing.T) {
	sqlDB, err := db.GetDB().DB()
	require.NoError(t, err)
	backend := NewSQLBackend(sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultSQLBackendConfig())
	require.NoError(t, backend.prepareStatements())
	t.Cleanup(backend.closeStatements)
	gatewayID := "seq-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM eventhub_gateway_states WHERE gateway_id = $1`, gatewayID)
	})

	for range 2 {
		require.NoError(t, backend.PublishEvent(context.Background(), gatewayID, Event{
			EventType: "llmprovider.deployed", Action: "CREATE", EntityID: "e",
		}))
	}

	rows, err := sqlDB.Query(`SELECT seq FROM eventhub_events WHERE gateway_id = $1 ORDER BY seq`, gatewayID)
	require.NoError(t, err)
	defer rows.Close()
	var seqs []int64
	for rows.Next() {
		var seq int64
		require.NoError(t, rows.Scan(&seq))
		seqs = append(seqs, seq)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []int64{1, 2}, seqs)

	var stateSeq int64
	require.NoError(t, sqlDB.QueryRow(`SELECT seq FROM eventhub_gateway_states WHERE gateway_id = $1`, gatewayID).
		Scan(&stateSeq))
	assert.EqualValues(t, 2, stateSeq)
}
