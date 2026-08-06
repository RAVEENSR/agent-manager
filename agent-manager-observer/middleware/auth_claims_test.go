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
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// unsignedToken builds an unsigned JWT (alg=none is rejected by the parser,
// so sign with a throwaway HMAC key; validateLocalDev never verifies).
func unsignedToken(t *testing.T, claims jwt.Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte("test-key"))
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}
	return s
}

func TestValidateLocalDev_ExtractsScopeClaim(t *testing.T) {
	token := unsignedToken(t, &TokenClaims{
		Sub:   "user-1",
		Scope: "amp:observability:trace-read amp:observability:log-read",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	claims, err := validateLocalDev(token)
	if err != nil {
		t.Fatalf("validateLocalDev returned error: %v", err)
	}
	if claims.Scope != "amp:observability:trace-read amp:observability:log-read" {
		t.Errorf("Scope = %q, want the two amp scopes", claims.Scope)
	}
	if claims.Sub != "user-1" {
		t.Errorf("Sub = %q, want %q", claims.Sub, "user-1")
	}
}

func TestValidateLocalDev_RejectsExpiredToken(t *testing.T) {
	token := unsignedToken(t, &TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	})
	if _, err := validateLocalDev(token); err == nil {
		t.Error("expected error for expired token, got nil")
	}
}

func TestTokenClaimsContextRoundTrip(t *testing.T) {
	in := &TokenClaims{Sub: "user-1", Scope: "amp:observability:trace-read"}
	ctx := ContextWithTokenClaims(context.Background(), in)
	out := GetTokenClaims(ctx)
	if out == nil || out.Sub != "user-1" || out.Scope != "amp:observability:trace-read" {
		t.Errorf("GetTokenClaims = %+v, want the stored claims", out)
	}
	if GetTokenClaims(context.Background()) != nil {
		t.Error("GetTokenClaims on empty context should return nil")
	}
}
