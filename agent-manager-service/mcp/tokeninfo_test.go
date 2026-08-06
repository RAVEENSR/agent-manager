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

package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/wso2/agent-manager/agent-manager-service/middleware/jwtassertion"
)

func TestClaimsTokenVerifierWithClaims(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	claims := &jwtassertion.TokenClaims{
		Sub:   "user-123",
		Scope: "amp:agent:read amp:project:read",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	ctx := jwtassertion.ContextWithTokenClaims(context.Background(), claims)

	info, err := claimsTokenVerifier(ctx, "ignored-raw-token", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
		return
	}
	if info == nil {
		t.Fatal("expected non-nil TokenInfo")
		return
	}
	wantScopes := []string{"amp:agent:read", "amp:project:read"}
	if len(info.Scopes) != len(wantScopes) {
		t.Fatalf("Scopes = %v, want %v", info.Scopes, wantScopes)
	}
	for i, s := range wantScopes {
		if info.Scopes[i] != s {
			t.Fatalf("Scopes = %v, want %v", info.Scopes, wantScopes)
		}
	}
	if info.UserID != claims.Sub {
		t.Errorf("UserID = %q, want %q", info.UserID, claims.Sub)
	}
	if !info.Expiration.Equal(claims.ExpiresAt.Time) {
		t.Errorf("Expiration = %v, want %v", info.Expiration, claims.ExpiresAt.Time)
	}
}

// TestClaimsTokenVerifierRecordsOrg proves the verifier stamps the caller's
// organization onto TokenInfo.Extra so authzMiddleware can confirm each
// per-request token targets the same org as the session (see mcp/tools/authz.go
// organization-consistency check).
func TestClaimsTokenVerifierRecordsOrg(t *testing.T) {
	claims := &jwtassertion.TokenClaims{
		Sub:   "user-123",
		Scope: "amp:agent:read",
		OuId:  "org-abc",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	ctx := jwtassertion.ContextWithTokenClaims(context.Background(), claims)

	info, err := claimsTokenVerifier(ctx, "ignored-raw-token", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := info.Extra["amp:ou-id"].(string); got != "org-abc" {
		t.Fatalf("TokenInfo.Extra[amp:ou-id] = %q, want %q", got, "org-abc")
	}
}

func TestClaimsTokenVerifierNoClaimsOnContext(t *testing.T) {
	_, err := claimsTokenVerifier(context.Background(), "any-token", nil)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err = %v, want wrapping auth.ErrInvalidToken", err)
	}
}

func TestClaimsTokenVerifierEmptySub(t *testing.T) {
	claims := &jwtassertion.TokenClaims{
		Sub:   "", // intentionally empty
		Scope: "amp:agent:read",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	ctx := jwtassertion.ContextWithTokenClaims(context.Background(), claims)

	_, err := claimsTokenVerifier(ctx, "ignored-raw-token", nil)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err = %v, want wrapping auth.ErrInvalidToken", err)
	}
}

func TestClaimsTokenVerifierNoExpiration(t *testing.T) {
	claims := &jwtassertion.TokenClaims{
		Sub:   "user-123",
		Scope: "amp:agent:read",
		// ExpiresAt intentionally left nil.
	}
	ctx := jwtassertion.ContextWithTokenClaims(context.Background(), claims)

	_, err := claimsTokenVerifier(ctx, "ignored-raw-token", nil)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err = %v, want wrapping auth.ErrInvalidToken", err)
	}
}
