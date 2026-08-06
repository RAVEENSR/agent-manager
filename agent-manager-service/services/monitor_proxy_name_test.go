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
}
