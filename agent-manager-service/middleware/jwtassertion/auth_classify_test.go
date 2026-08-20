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

package jwtassertion

import (
	"errors"
	"fmt"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// TestClassifyAuthFailureUsesSentinels covers every label the audit trail can
// carry, from both sources that produce one: this package's own sentinels and
// the jwt library's.
func TestClassifyAuthFailureUsesSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "unknown"},
		{"own expiry", fmt.Errorf("%w", ErrTokenExpired), "expired"},
		{"library expiry", fmt.Errorf("failed to parse token: %w", jwt.ErrTokenExpired), "expired"},
		{"own issuer", fmt.Errorf("%w: got %s", ErrBadIssuer, "https://elsewhere.example"), "bad-issuer"},
		{"library issuer", fmt.Errorf("wrapped: %w", jwt.ErrTokenInvalidIssuer), "bad-issuer"},
		{"own audience", fmt.Errorf("%w: got %v", ErrBadAudience, []string{"other"}), "bad-audience"},
		{"library audience", fmt.Errorf("wrapped: %w", jwt.ErrTokenInvalidAudience), "bad-audience"},
		{"unknown kid", fmt.Errorf("%w: kid has an invalid format", ErrUnknownKid), "unknown-kid"},
		{"own signature", fmt.Errorf("%w: unexpected signing method: HS256", ErrBadSignature), "bad-signature"},
		{"library signature", fmt.Errorf("wrapped: %w", jwt.ErrTokenSignatureInvalid), "bad-signature"},
		{"malformed", fmt.Errorf("%w: failed to extract claims", ErrMalformedToken), "malformed"},
		{"library malformed", fmt.Errorf("wrapped: %w", jwt.ErrTokenMalformed), "malformed"},
		{"not valid", fmt.Errorf("%w", ErrTokenInvalid), "invalid"},
		{"library not yet valid", fmt.Errorf("wrapped: %w", jwt.ErrTokenNotValidYet), "invalid"},
		{"not configured", fmt.Errorf("%w: configuration not loaded", ErrAuthNotConfigured), "server-error"},
		{"key set unavailable", fmt.Errorf("%w: failed to fetch JWKS: %w", ErrKeySetUnavailable, errors.New("dial tcp: refused")), "server-error"},
		{"unrecognised", errors.New("something else entirely"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyAuthFailure(tt.err); got != tt.want {
				t.Errorf("classifyAuthFailure(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// TestClassifyAuthFailureIgnoresClaimText is the regression this whole change
// exists for. validateIssuer and validateAudience interpolate the token's own
// claim values into the rejection message, so classifying on message text let
// the token pick its own audit label: an issuer containing "expired" was
// recorded as a routine expiry instead of as a rejected issuer, hiding an
// attack signal behind the most ignorable label in the set.
func TestClassifyAuthFailureIgnoresClaimText(t *testing.T) {
	tests := []struct {
		name    string
		claim   string
		produce func(string) error
		want    string
	}{
		{
			name:    "issuer containing expired",
			claim:   "https://expired.tokens.example",
			produce: func(c string) error { return validateIssuer(c, []string{"https://trusted.example"}) },
			want:    "bad-issuer",
		},
		{
			name:    "issuer containing signature",
			claim:   "https://signature.example",
			produce: func(c string) error { return validateIssuer(c, []string{"https://trusted.example"}) },
			want:    "bad-issuer",
		},
		{
			name:    "issuer containing kid",
			claim:   "https://kid.example",
			produce: func(c string) error { return validateIssuer(c, []string{"https://trusted.example"}) },
			want:    "bad-issuer",
		},
		{
			name:    "audience containing expired",
			claim:   "expired-service",
			produce: func(c string) error { return validateAudience(jwt.ClaimStrings{c}, []string{"amp"}) },
			want:    "bad-audience",
		},
		{
			name:    "audience containing kid",
			claim:   "kid-service",
			produce: func(c string) error { return validateAudience(jwt.ClaimStrings{c}, []string{"amp"}) },
			want:    "bad-audience",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.produce(tt.claim)
			if err == nil {
				t.Fatalf("expected %q to be rejected", tt.claim)
			}
			if got := classifyAuthFailure(err); got != tt.want {
				t.Errorf("claim %q classified as %q, want %q — the label must come "+
					"from the sentinel, not from text the token controls", tt.claim, got, tt.want)
			}
		})
	}
}

// TestAuthFailureReasonsCarryNoTokenMaterial guards the property the audit
// trail depends on: the label is drawn from a closed set, so no claim value,
// header or token fragment can reach a record through it.
func TestAuthFailureReasonsCarryNoTokenMaterial(t *testing.T) {
	allowed := map[string]bool{
		"expired": true, "bad-issuer": true, "bad-audience": true,
		"unknown-kid": true, "bad-signature": true, "malformed": true,
		"invalid": true, "server-error": true, "unknown": true,
	}

	secret := "eyJhbGciOiJSUzI1NiJ9.super-secret-token-material"
	errs := []error{
		validateIssuer(secret, []string{"https://trusted.example"}),
		validateAudience(jwt.ClaimStrings{secret}, []string{"amp"}),
		fmt.Errorf("%w: %s", ErrUnknownKid, secret),
		fmt.Errorf("failed to parse token: %w", jwt.ErrTokenMalformed),
		errors.New(secret),
	}

	for _, err := range errs {
		reason := classifyAuthFailure(err)
		if !allowed[reason] {
			t.Errorf("classifyAuthFailure produced unlisted reason %q", reason)
		}
		if reason == secret || len(reason) > 20 {
			t.Errorf("reason %q looks like it carries token material", reason)
		}
	}
}
