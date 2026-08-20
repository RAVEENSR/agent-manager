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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/repositories/repomocks"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// clearGatewayManifestCache resets the process-wide backend to a fresh in-memory
// cache, undoing both any Set() from the test and any SetGatewayManifestCacheBackend
// swap another test may have left behind.
func clearGatewayManifestCache(t *testing.T) {
	t.Helper()
	original := gatewayManifestCache
	t.Cleanup(func() { gatewayManifestCache = original })
	gatewayManifestCache = NewInMemoryGatewayManifestCache()
}

// TestSaveGatewayPolicyManifestCachesWithoutWritingRow is the point of moving manifests
// out of the jsonb column: a push must land in the cache and must not update the row.
func TestSaveGatewayPolicyManifestCachesWithoutWritingRow(t *testing.T) {
	clearGatewayManifestCache(t)

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
	require.NoError(t, svc.SaveGatewayPolicyManifest(context.Background(), gateway.UUID.String(), manifest))

	cached, ok, err := gatewayManifestCache.Get(context.Background())
	require.NoError(t, err)
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
	clearGatewayManifestCache(t)

	repo := &repomocks.GatewayRepositoryMock{
		GetByUUIDFunc: func(string) (*models.Gateway, error) {
			//nolint:nilnil // GetByUUID reports "no such gateway" as (nil, nil).
			return nil, nil
		},
	}
	svc := NewPlatformGatewayService(repo, nil)

	err := svc.SaveGatewayPolicyManifest(context.Background(), "11111111-1111-1111-1111-111111111111", map[string]interface{}{})
	require.ErrorIs(t, err, utils.ErrGatewayNotFound)

	_, ok, getErr := gatewayManifestCache.Get(context.Background())
	require.NoError(t, getErr)
	require.False(t, ok)
}

// TestGatewayManifestFallsBackToRow covers the cold window after a restart: until some
// gateway pushes again, the manifest still on the row is what readers evaluate.
func TestGatewayManifestFallsBackToRow(t *testing.T) {
	clearGatewayManifestCache(t)

	gateway := newGateway(t, models.GatewayRoleBoth, true)
	gateway.Manifest = map[string]interface{}{"policies": []interface{}{}}

	require.Equal(t, gateway.Manifest, gatewayManifest(gateway))
	require.Nil(t, gatewayManifest(nil))
}

// TestGatewayManifestCacheKeepsOnlyLatest documents the single-copy contract: a second
// push replaces the first rather than accumulating.
func TestGatewayManifestCacheKeepsOnlyLatest(t *testing.T) {
	ctx := context.Background()
	cache := NewInMemoryGatewayManifestCache()
	first := map[string]interface{}{"policies": []interface{}{"a"}}
	second := map[string]interface{}{"policies": []interface{}{"b"}}

	require.NoError(t, cache.Set(ctx, first))
	require.NoError(t, cache.Set(ctx, second))

	cached, ok, err := cache.Get(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, second, cached)

	require.NoError(t, cache.Clear(ctx))
	_, ok, err = cache.Get(ctx)
	require.NoError(t, err)
	require.False(t, ok)
}

// TestGatewayManifest_CacheReadErrorFallsBackToRow covers a Redis-unreachable-style
// failure: gatewayManifest must degrade to the row rather than propagate the error,
// since manifests are advisory.
func TestGatewayManifest_CacheReadErrorFallsBackToRow(t *testing.T) {
	clearGatewayManifestCache(t)
	gatewayManifestCache = &failingManifestCacheBackend{}

	gateway := newGateway(t, models.GatewayRoleBoth, true)
	gateway.Manifest = map[string]interface{}{"policies": []interface{}{}}

	require.Equal(t, gateway.Manifest, gatewayManifest(gateway))
}

// failingManifestCacheBackend simulates an unreachable external cache backend.
type failingManifestCacheBackend struct{}

func (f *failingManifestCacheBackend) Set(context.Context, map[string]interface{}) error {
	return errCacheUnavailable
}

func (f *failingManifestCacheBackend) Get(context.Context) (map[string]interface{}, bool, error) {
	return nil, false, errCacheUnavailable
}

func (f *failingManifestCacheBackend) Clear(context.Context) error {
	return errCacheUnavailable
}

var errCacheUnavailable = errors.New("cache backend unavailable")
