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

package framework

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Token crafting for authentication tests.
//
// Deliberately stdlib-only: adding a JWT library to the e2e module just to
// build deliberately-invalid tokens would be a dependency the production code
// does not have, and hand-rolling is both trivial and more explicit about what
// is being forged.
//
// A note on what these can and cannot prove. The service validates a token in
// this order (middleware/jwtassertion/auth.go): RSA signing method -> kid
// lookup in the JWKS -> signature -> registered claims (exp) -> issuer ->
// audience. Since the tests cannot sign with Thunder's private key, a token
// crafted with a bad exp or audience is rejected at the *signature* step, not
// the claim step. So these specs prove "rejected with 401", not "rejected
// because of that specific claim". That is the security-relevant property; the
// per-claim logic is unit-tested in middleware/jwtassertion/auth_test.go.
//
// The exception, and the highest-value case here, is TamperClaims: it keeps a
// genuine signature and rewrites the payload. That is the real attack — escalate
// your own token by editing its scopes — and it is exactly assertable.

// jwtPart base64url-encodes v as JSON, without padding, as a JWT segment.
func jwtPart(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal jwt segment: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodeTokenClaims returns a token's payload as a generic map, without
// verifying anything.
func DecodeTokenClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a JWT: expected 3 parts, got %d", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	return claims, nil
}

// TamperClaims rewrites a genuine token's payload via mutate, keeping the
// original header and signature. The signature no longer matches, so the
// server must reject it — this is the canonical privilege-escalation attempt
// (edit your own scopes and replay).
func TamperClaims(token string, mutate func(claims map[string]any)) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("not a JWT: expected 3 parts, got %d", len(parts))
	}

	claims, err := DecodeTokenClaims(token)
	if err != nil {
		return "", err
	}
	mutate(claims)

	payload, err := jwtPart(claims)
	if err != nil {
		return "", err
	}
	return parts[0] + "." + payload + "." + parts[2], nil
}

// TamperHeader rewrites a genuine token's header via mutate, keeping the
// original payload and signature. Use to forge a kid or an alg.
func TamperHeader(token string, mutate func(header map[string]any)) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("not a JWT: expected 3 parts, got %d", len(parts))
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode header: %w", err)
	}
	var header map[string]any
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", fmt.Errorf("unmarshal header: %w", err)
	}
	mutate(header)

	encoded, err := jwtPart(header)
	if err != nil {
		return "", err
	}
	return encoded + "." + parts[1] + "." + parts[2], nil
}

// UnsignedToken builds an `alg: none` token with the given claims and an empty
// signature. The service must reject it: validateJWTWithJWKS requires an RSA
// signing method before it looks at anything else.
func UnsignedToken(claims map[string]any) (string, error) {
	header, err := jwtPart(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := jwtPart(claims)
	if err != nil {
		return "", err
	}
	return header + "." + payload + ".", nil
}

// HS256Token builds a token signed with HMAC-SHA256 under an attacker-chosen
// key. The service must reject it on signing method alone — an implementation
// that accepted a symmetric algorithm would let anyone who can read the public
// JWKS mint valid tokens.
func HS256Token(claims map[string]any, key string) (string, error) {
	header, err := jwtPart(map[string]any{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := jwtPart(claims)
	if err != nil {
		return "", err
	}

	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sig, nil
}

// PlausibleClaims returns a claim set shaped like a real AMP token, for use
// with UnsignedToken / HS256Token. Overrides are applied last, so a spec can
// set exp in the past or swap the audience.
func PlausibleClaims(cfg *Config, overrides map[string]any) map[string]any {
	now := time.Now()
	claims := map[string]any{
		"sub":      "sec-test-forged-subject",
		"scope":    strings.Join(AllScopes(), " "),
		"ouId":     "forged-ou-id",
		"ouHandle": cfg.DefaultOrg,
		"iat":      now.Unix(),
		"exp":      now.Add(time.Hour).Unix(),
		"aud":      []string{"amp"},
		"iss":      "http://thunder.amp.localhost:8080",
	}
	for k, v := range overrides {
		claims[k] = v
	}
	return claims
}
