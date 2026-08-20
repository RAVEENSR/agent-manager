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

package middleware

import (
	"context"
	"testing"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
)

// TestCoalesceKeyDistinguishesGatewaysBehindOneAddress is the regression for a
// suppression path that lost records.
//
// The bulk-sync routes are on the internal gateway server, which carries no
// token: audit.Source is built before the handler runs, so its ActorID is
// empty, and it is the handler that authenticates the gateway. The coalescing
// key therefore fell through to the source IP — so two gateways sharing an
// egress address suppressed each other, and the records exist precisely to say
// which gateway pulled key material.
func TestCoalesceKeyDistinguishesGatewaysBehindOneAddress(t *testing.T) {
	const sharedIP = "10.0.0.7"

	newRequest := func(gatewayID string) (context.Context, *audit.RequestScope) {
		ctx := audit.WithSource(context.Background(), audit.Source{
			Surface: audit.SurfaceInternal,
			IP:      sharedIP,
			// ActorID deliberately empty: this is the state WithAudit leaves,
			// because the handler has not authenticated anyone yet.
		})
		ctx, scope := audit.NewRequestScope(ctx)
		if gatewayID != "" {
			audit.IdentifyActor(ctx, audit.ActorGateway, gatewayID, "api-key")
		}
		return ctx, scope
	}

	ctxA, scopeA := newRequest("gateway-a")
	ctxB, scopeB := newRequest("gateway-b")

	keyA := sourceKeyOf(ctxA, scopeA)
	keyB := sourceKeyOf(ctxB, scopeB)

	if keyA == keyB {
		t.Fatalf("both gateways coalesce under the same key %q; one would suppress the other", keyA)
	}
	if keyA != "gateway-a" || keyB != "gateway-b" {
		t.Errorf("keys = %q, %q; want the authenticated gateway ids", keyA, keyB)
	}
	if keyA == sharedIP || keyB == sharedIP {
		t.Error("key fell back to the shared source IP despite an identified gateway")
	}
}

// TestCoalesceKeyFallsBackToAddressWhenUnidentified keeps the fallback: a
// request that never authenticated has nothing better to be keyed on, and
// suppressing a flood of those by address is the wanted behaviour.
func TestCoalesceKeyFallsBackToAddressWhenUnidentified(t *testing.T) {
	ctx := audit.WithSource(context.Background(), audit.Source{
		Surface: audit.SurfaceInternal,
		IP:      "10.0.0.9",
	})
	ctx, scope := audit.NewRequestScope(ctx)

	if got := sourceKeyOf(ctx, scope); got != "10.0.0.9" {
		t.Errorf("sourceKeyOf = %q, want the source IP when nothing identified the caller", got)
	}
}

// TestCoalesceKeyPrefersTheTokenSubjectOnAuthenticatedSurfaces confirms the
// token-bearing surfaces are unaffected: Source.ActorID still keys them when
// no handler identified anyone.
func TestCoalesceKeyPrefersTheTokenSubjectOnAuthenticatedSurfaces(t *testing.T) {
	ctx := audit.WithSource(context.Background(), audit.Source{
		Surface: audit.SurfaceAPI,
		IP:      "10.0.0.11",
		ActorID: "service-account-1",
	})
	ctx, scope := audit.NewRequestScope(ctx)

	if got := sourceKeyOf(ctx, scope); got != "service-account-1" {
		t.Errorf("sourceKeyOf = %q, want the Source actor", got)
	}
}

// TestIdentifyActorOutsideARequestIsANoop guards the emit-site contract: the
// same call made from a background worker, where no scope exists, must not
// panic.
func TestIdentifyActorOutsideARequestIsANoop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IdentifyActor panicked outside a request: %v", r)
		}
	}()
	audit.IdentifyActor(context.Background(), audit.ActorGateway, "gateway-a", "api-key")
}
