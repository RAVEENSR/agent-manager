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
	"sync"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// GatewayManifestCache holds the latest policy manifest pushed by a gateway.
//
// Manifests are large and every gateway re-pushes its whole manifest on a fixed
// heartbeat, so persisting them wrote a multi-KB jsonb blob per push for data that is
// only ever read to answer "which policies do the gateways advertise?". Every gateway
// in a deployment runs the same policy bundle, so the cache keeps exactly one copy for
// all of them: the most recent push replaces the previous one, no per-gateway keying,
// no history and no TTL. Deleting a gateway therefore leaves the cache alone — the
// remaining gateways still report that same manifest.
//
// The cache is in-process: a restarted (or newly scaled-up) replica starts empty and
// refills on the next push from any gateway. Manifests are advisory (they gate policy
// pickers, not traffic), so a cold window is tolerable.
type GatewayManifestCache struct {
	mu       sync.RWMutex
	manifest map[string]interface{}
	set      bool
}

// NewGatewayManifestCache creates an empty manifest cache.
func NewGatewayManifestCache() *GatewayManifestCache {
	return &GatewayManifestCache{
		mu:       sync.RWMutex{},
		manifest: nil,
		set:      false,
	}
}

// Set replaces the cached manifest with the one just pushed.
func (c *GatewayManifestCache) Set(manifest map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.manifest = manifest
	c.set = true
}

// Get returns the cached manifest, and whether one has been pushed since startup.
func (c *GatewayManifestCache) Get() (map[string]interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.manifest, c.set
}

// Clear drops the cached manifest, so reads fall back to the gateway rows again.
func (c *GatewayManifestCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.manifest = nil
	c.set = false
}

// gatewayManifestCache is the process-wide manifest cache. It is shared because the
// writer (the gateway manifest push endpoint) and the readers (the MCP and LLM policy
// pickers) sit in different services that are constructed independently.
var gatewayManifestCache = NewGatewayManifestCache()

// gatewayManifest returns the manifest to evaluate for a gateway: the cached copy once
// any gateway has pushed since this replica started, otherwise the manifest still on
// this gateway's row. The fallback covers the cold window (and rows written before
// manifests moved out of the database); it stops being used once the first push lands.
func gatewayManifest(gateway *models.Gateway) map[string]interface{} {
	if manifest, ok := gatewayManifestCache.Get(); ok {
		return manifest
	}
	if gateway == nil {
		return nil
	}

	return gateway.Manifest
}
