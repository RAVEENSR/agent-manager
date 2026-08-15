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

	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// TestSaveGatewayPolicyManifestCachesWithoutWritingRow is the point of moving manifests
// out of the jsonb column: a push must land in the cache and must not update the row.
func TestSaveGatewayPolicyManifestCachesWithoutWritingRow(t *testing.T) {
	t.Cleanup(gatewayManifestCache.Clear)

	gateway := newGateway(t, models.GatewayRoleBoth, true)
	repo := &repomocks.GatewayRepositoryMock{
		GetByUUIDFunc: func(gatewayID string) (*models.Gateway, error) {
			require.Equal(t, gateway.UUID.String(), gatewayID)
			return gateway, nil
		},
		// UpdateGatewayFunc is left nil: calling it panics, which is the assertion that
		// the push no longer writes the row.
	}
	svc := NewPlatformGatewayService(repo, nil)

	manifest := map[string]interface{}{
		"policies": []interface{}{
			map[string]interface{}{"name": "mcp-auth", "version": "v1"},
		},
	}
	require.NoError(t, svc.SaveGatewayPolicyManifest(gateway.UUID.String(), manifest))

	cached, ok := gatewayManifestCache.Get()
	require.True(t, ok)
	require.Equal(t, manifest, cached)

	// The one cached copy answers for every gateway, including ones whose row still
	// carries an older manifest.
	require.Equal(t, manifest, gatewayManifest(gateway))
	require.Equal(t, manifest, gatewayManifest(newGateway(t, models.GatewayRoleEgress, true)))
}

// TestSaveGatewayPolicyManifestUnknownGateway keeps the endpoint's 404 behaviour: an
// unknown gateway must not seed the cache.
func TestSaveGatewayPolicyManifestUnknownGateway(t *testing.T) {
	t.Cleanup(gatewayManifestCache.Clear)

	repo := &repomocks.GatewayRepositoryMock{
		GetByUUIDFunc: func(string) (*models.Gateway, error) {
			//nolint:nilnil // GetByUUID reports "no such gateway" as (nil, nil).
			return nil, nil
		},
	}
	svc := NewPlatformGatewayService(repo, nil)

	err := svc.SaveGatewayPolicyManifest("11111111-1111-1111-1111-111111111111", map[string]interface{}{})
	require.ErrorIs(t, err, utils.ErrGatewayNotFound)

	_, ok := gatewayManifestCache.Get()
	require.False(t, ok)
}

// TestGatewayManifestFallsBackToRow covers the cold window after a restart: until some
// gateway pushes again, the manifest still on the row is what readers evaluate.
func TestGatewayManifestFallsBackToRow(t *testing.T) {
	t.Cleanup(gatewayManifestCache.Clear)

	gateway := newGateway(t, models.GatewayRoleBoth, true)
	gateway.Manifest = map[string]interface{}{"policies": []interface{}{}}

	require.Equal(t, gateway.Manifest, gatewayManifest(gateway))
	require.Nil(t, gatewayManifest(nil))
}

// TestGatewayManifestCacheKeepsOnlyLatest documents the single-copy contract: a second
// push replaces the first rather than accumulating.
func TestGatewayManifestCacheKeepsOnlyLatest(t *testing.T) {
	cache := NewGatewayManifestCache()
	first := map[string]interface{}{"policies": []interface{}{"a"}}
	second := map[string]interface{}{"policies": []interface{}{"b"}}

	cache.Set(first)
	cache.Set(second)

	cached, ok := cache.Get()
	require.True(t, ok)
	require.Equal(t, second, cached)

	cache.Clear()
	_, ok = cache.Get()
	require.False(t, ok)
}
