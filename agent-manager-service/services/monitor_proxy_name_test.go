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
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestMonitorProxyName guards the collision fix: the derived proxy name must be
// unique per monitor (via the monitor UUID) so two monitors with the same name
// and provider — e.g. a "monitor-1"/"openai" on two different agents — do not
// derive the same handle and fail provisioning with "LLM proxy already exists".
// It also stays within the Kubernetes name budget (<=52 so "-deployment" keeps
// it <=63).
func TestMonitorProxyName(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	// id2 deliberately shares id1's first eight hex digits and differs only
	// afterwards, so a suffix built from a truncated UUID prefix would collide.
	id2 := uuid.MustParse("11111111-1111-1111-1111-111111111112")

	t.Run("includes the monitor uuid, ends in -proxy, within budget", func(t *testing.T) {
		name := monitorProxyName(id1, "monitor-1", "openai")
		if !strings.HasSuffix(name, "-proxy") {
			t.Errorf("expected -proxy suffix, got %q", name)
		}
		if !strings.Contains(name, "11111111") {
			t.Errorf("expected monitor uuid prefix in %q", name)
		}
		if len(name) > 52 {
			t.Errorf("proxy name too long: %q (len=%d, max=52)", name, len(name))
		}
	})

	t.Run("same name and provider but different monitors do not collide", func(t *testing.T) {
		a := monitorProxyName(id1, "monitor-1", "openai")
		b := monitorProxyName(id2, "monitor-1", "openai")
		if a == b {
			t.Errorf("expected distinct proxy names for different monitors, both were %q", a)
		}
	})

	t.Run("long names truncate but keep the uuid, suffix, budget, and distinctness", func(t *testing.T) {
		longName := strings.Repeat("a", 60)
		longProvider := "some-very-long-provider-name"

		name := monitorProxyName(id1, longName, longProvider)
		if len(name) > 52 {
			t.Errorf("proxy name too long: %q (len=%d, max=52)", name, len(name))
		}
		if !strings.HasSuffix(name, "-proxy") {
			t.Errorf("expected -proxy suffix, got %q", name)
		}
		if !strings.Contains(name, "11111111") {
			t.Errorf("expected monitor uuid prefix in %q", name)
		}

		other := monitorProxyName(id2, longName, longProvider)
		if name == other {
			t.Errorf("expected distinct truncated names for different monitors, both were %q", name)
		}
	})

	// Regression for issue #1670: switching providers used to derive the same
	// handle as the still-live old proxy, failing with "LLM proxy already exists".
	t.Run("same monitor, different provider does not collide (issue #1670)", func(t *testing.T) {
		cases := []struct {
			name, monitorName, providerA, providerB string
		}{
			{"live repro name", "repro-monitor", "openai", "anthropic"},
			{"another live repro name", "test-monitor-llm", "openai", "anthropic"},
			{
				"long, near-identical monitor and provider names", "this is monitor use to test 01",
				"openai-llm-provider-01", "openai-llm-provider-02",
			},
		}
		for _, c := range cases {
			a := monitorProxyName(id1, c.monitorName, c.providerA)
			b := monitorProxyName(id1, c.monitorName, c.providerB)
			if a == b {
				t.Errorf("%s: expected distinct proxy names when only the provider changes, both were %q", c.name, a)
			}
			if len(a) > 52 || len(b) > 52 {
				t.Errorf("%s: proxy name too long: %q (%d) / %q (%d), max=52", c.name, a, len(a), b, len(b))
			}
		}
	})

	// Two monitors and two providers, all four combinations: no cell in the
	// (monitor x provider) grid should collide with any other.
	t.Run("full grid of monitors x providers stays distinct", func(t *testing.T) {
		monitors := []struct {
			id   uuid.UUID
			name string
		}{
			{id1, "this is monitor use to test 01"},
			{id2, "this is monitor use to test 02"},
		}
		providers := []string{"openai-llm-provider-01", "openai-llm-provider-02"}

		seen := make(map[string]string)
		for _, m := range monitors {
			for _, p := range providers {
				h := monitorProxyName(m.id, m.name, p)
				key := m.name + "|" + p
				if prevKey, ok := seen[h]; ok {
					t.Errorf("collision: %q and %q both produced %q", prevKey, key, h)
				}
				seen[h] = key
			}
		}
	})
}
