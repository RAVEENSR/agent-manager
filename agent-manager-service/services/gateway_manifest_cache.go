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
	"sync"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// GatewayManifestCacheBackend holds the latest policy manifest pushed by a gateway.
//
// Manifests are large and every gateway re-pushes its whole manifest on a fixed
// heartbeat, so persisting them wrote a multi-KB jsonb blob per push for data that is
// only ever read to answer "which policies do the gateways advertise?". Every gateway
// in a deployment runs the same policy bundle, so the cache keeps exactly one copy for
// all of them: the most recent push replaces the previous one, no per-gateway keying,
// no history and no TTL. Deleting a gateway therefore leaves the cache alone — the
// remaining gateways still report that same manifest.
//
// Two implementations exist:
//   - InMemoryGatewayManifestCache: process-local, the default. A restarted (or newly
//     scaled-up) replica starts empty and refills on the next push from any gateway.
//   - RedisGatewayManifestCache: shared across replicas. Required in HA deployments —
//     an in-process cache is inconsistent there, since each replica only ever sees the
//     pushes routed to it, and readers on other replicas would see a stale or empty
//     cache for gateways that never pushed to them.
//
// Manifests are advisory (they gate policy pickers, not traffic), so a cold window on
// either backend is tolerable.
type GatewayManifestCacheBackend interface {
	// Set replaces the cached manifest with the one just pushed.
	Set(ctx context.Context, manifest map[string]interface{}) error
	// Get returns the cached manifest, and whether one has been pushed since startup
	// (in-memory backend) or since the key was last cleared/evicted (Redis backend).
	Get(ctx context.Context) (map[string]interface{}, bool, error)
	// Clear drops the cached manifest, so reads fall back to the gateway rows again.
	Clear(ctx context.Context) error
}

// InMemoryGatewayManifestCache is the process-local GatewayManifestCacheBackend. Safe
// as the default for a single replica; under HA, each replica gets its own independent
// copy, which readers on other replicas cannot see — use RedisGatewayManifestCache there.
type InMemoryGatewayManifestCache struct {
	mu       sync.RWMutex
	manifest map[string]interface{}
	set      bool
}

// NewInMemoryGatewayManifestCache creates an empty in-process manifest cache.
func NewInMemoryGatewayManifestCache() *InMemoryGatewayManifestCache {
	return &InMemoryGatewayManifestCache{
		mu:       sync.RWMutex{},
		manifest: nil,
		set:      false,
	}
}

// Set implements GatewayManifestCacheBackend.
func (c *InMemoryGatewayManifestCache) Set(_ context.Context, manifest map[string]interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.manifest = manifest
	c.set = true
	return nil
}

// Get implements GatewayManifestCacheBackend.
func (c *InMemoryGatewayManifestCache) Get(_ context.Context) (map[string]interface{}, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.manifest, c.set, nil
}

// Clear implements GatewayManifestCacheBackend.
func (c *InMemoryGatewayManifestCache) Clear(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.manifest = nil
	c.set = false
	return nil
}

// gatewayManifestCache is the process-wide manifest cache backend. It is a package
// var — rather than constructor-injected into every caller — because the writer (the
// gateway manifest push endpoint, via PlatformGatewayService) and the readers (the MCP
// and LLM policy pickers, package-level functions in this package) are constructed
// independently and neither currently threads a shared dependency between them.
// Overwritten at process startup by wiring.ProvideGatewayManifestCacheBackend once
// config selects between "memory" and "redis"; defaults to the in-memory backend so
// tests and any code path that runs before wiring still get a working cache.
var gatewayManifestCache GatewayManifestCacheBackend = NewInMemoryGatewayManifestCache()

// SetGatewayManifestCacheBackend swaps the process-wide manifest cache backend. Called
// once at startup by wiring, based on config.GatewayManifestCache.Backend.
func SetGatewayManifestCacheBackend(backend GatewayManifestCacheBackend) {
	gatewayManifestCache = backend
}

// gatewayManifest returns the manifest to evaluate for a gateway: the cached copy once
// any gateway has pushed since this replica started (or, with the Redis backend, since
// any replica last pushed), otherwise the manifest still on this gateway's row. The
// fallback covers the cold window (and rows written before manifests moved out of the
// database); it stops being used once the first push lands. A cache read failure (e.g.
// Redis unreachable) degrades to the same row fallback rather than erroring the whole
// policy listing — manifests are advisory, so a stale read beats a hard failure.
func gatewayManifest(gateway *models.Gateway) map[string]interface{} {
	manifest, ok, err := gatewayManifestCache.Get(context.Background())
	if err == nil && ok {
		return manifest
	}
	if gateway == nil {
		return nil
	}

	return gateway.Manifest
}
