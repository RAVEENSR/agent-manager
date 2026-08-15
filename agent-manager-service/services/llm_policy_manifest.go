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
	"fmt"
	"sort"
	"strings"

	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// llmPolicyManifestItem is one guardrail policy definition reported by a gateway's
// pushed manifest. Scoped to LLM guardrail listing only — intentionally independent
// from MCP proxy's own manifest walk in mcp_proxy_service.go, which has different
// availability semantics (hub-intersected) and must not be affected by this endpoint.
type llmPolicyManifestItem struct {
	Name             string
	Version          string
	DisplayName      string
	Description      string
	Parameters       map[string]interface{}
	SystemParameters map[string]interface{}
}

// intersectActiveGatewayLLMPolicies returns, keyed by "name\x00version", the full policy
// definitions reported by EVERY active gateway in the org — a policy is "available" only
// if all active gateways advertise it.
func intersectActiveGatewayLLMPolicies(gatewayRepo repositories.GatewayRepository, orgUUID string) (map[string]llmPolicyManifestItem, error) {
	if gatewayRepo == nil {
		return map[string]llmPolicyManifestItem{}, nil
	}

	active := true
	gateways, err := gatewayRepo.ListWithFilters(repositories.GatewayFilterOptions{
		OrganizationID: orgUUID,
		Status:         &active,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list active gateways: %w", err)
	}

	var available map[string]llmPolicyManifestItem
	seenGateway := false
	for _, gateway := range gateways {
		if gateway == nil {
			continue
		}
		gatewayPolicies := map[string]llmPolicyManifestItem{}
		for _, policy := range extractLLMPolicyManifestItems(gatewayManifest(gateway)) {
			if policy.Name == "" || policy.Version == "" {
				continue
			}
			key := policy.Name + "\x00" + policy.Version
			gatewayPolicies[key] = policy
		}
		if !seenGateway {
			available = gatewayPolicies
			seenGateway = true
			continue
		}
		for key := range available {
			if _, ok := gatewayPolicies[key]; !ok {
				delete(available, key)
			}
		}
	}

	if available == nil {
		available = map[string]llmPolicyManifestItem{}
	}
	return available, nil
}

// sortedLLMPolicyManifestItems returns the map's values sorted by Name then Version,
// for a stable API response.
func sortedLLMPolicyManifestItems(available map[string]llmPolicyManifestItem) []llmPolicyManifestItem {
	items := make([]llmPolicyManifestItem, 0, len(available))
	for _, item := range available {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].Version < items[j].Version
		}
		return items[i].Name < items[j].Name
	})
	return items
}

// extractLLMPolicyManifestItems recursively walks a gateway's arbitrarily-shaped,
// self-reported manifest JSON and pulls out every policy definition it can find,
// including the metadata (displayName/description/parameters/systemParameters) the
// LLM guardrail picker needs to render without a policy-hub round-trip. It tolerates
// a few different key names ("name"/"policyName"/"id", "version"/"policyVersion")
// since the manifest shape is not schema-enforced across gateway versions.
func extractLLMPolicyManifestItems(value interface{}) []llmPolicyManifestItem {
	seen := map[string]struct{}{}
	items := make([]llmPolicyManifestItem, 0)
	var walk func(interface{})

	stringValue := func(v interface{}) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}

	mapValue := func(v interface{}) map[string]interface{} {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
		return nil
	}

	lookup := func(values map[string]interface{}, keys ...string) interface{} {
		for _, key := range keys {
			if value, ok := values[key]; ok {
				return value
			}
		}
		return nil
	}

	add := func(entry map[string]interface{}, name, version string) {
		name = strings.TrimSpace(name)
		version = strings.TrimSpace(version)
		if name == "" || version == "" {
			return
		}
		key := name + "\x00" + version
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, llmPolicyManifestItem{
			Name:             name,
			Version:          version,
			DisplayName:      stringValue(lookup(entry, "displayName")),
			Description:      stringValue(lookup(entry, "description")),
			Parameters:       mapValue(lookup(entry, "parameters")),
			SystemParameters: mapValue(lookup(entry, "systemParameters")),
		})
	}

	// consumedKeys are a policy's own metadata / JSON-Schema leaves — never
	// containers of OTHER policies. They're skipped during the generic recursive
	// descent so a nested schema object carrying coincidental name+version keys
	// (e.g. a default example {"name":"gpt-4","version":"1.0"} inside a parameter
	// schema) isn't surfaced as a bogus, unrelated policy.
	consumedKeys := map[string]struct{}{
		"parameters":       {},
		"systemParameters": {},
		"displayName":      {},
		"description":      {},
	}

	walk = func(current interface{}) {
		switch typed := current.(type) {
		case []interface{}:
			for _, item := range typed {
				walk(item)
			}
		case []map[string]interface{}:
			for _, item := range typed {
				walk(item)
			}
		case map[string]interface{}:
			name := stringValue(lookup(typed, "name", "policyName", "id"))
			version := stringValue(lookup(typed, "version", "policyVersion"))
			if name != "" && version != "" {
				add(typed, name, version)
			}
			if name != "" {
				if versions, ok := lookup(typed, "versions", "policyVersions").([]interface{}); ok {
					for _, rawVersion := range versions {
						add(typed, name, stringValue(rawVersion))
					}
				}
				if versions, ok := lookup(typed, "versions", "policyVersions").([]string); ok {
					for _, rawVersion := range versions {
						add(typed, name, rawVersion)
					}
				}
			}
			for key, nested := range typed {
				if _, skip := consumedKeys[key]; skip {
					continue
				}
				walk(nested)
			}
		}
	}

	walk(value)
	return items
}
