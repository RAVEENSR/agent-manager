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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Scope reduction
//
// Thunder issues a client_credentials token carrying requested ∩ allowed
// scopes (see the ampScopes comment in auth.go). The e2e IDP client is allowed
// the full amp: superset, so asking for a SUBSET yields a genuinely
// under-privileged token — no extra users, roles, or IDP clients needed. That
// is the whole negative-authorization harness for the security suite.
//
// Callers must not assume the reduction worked: TokenScopes decodes what the
// IDP actually issued, and the security suite asserts on it before running any
// spec (a silently-unreduced token would make every negative test vacuous).

// AllScopes returns every amp: scope the e2e IDP client requests for a
// full-privilege token, as a fresh slice.
func AllScopes() []string {
	return strings.Fields(ampScopes)
}

// ScopesExcept returns AllScopes minus the given scopes. Scopes are the full
// "amp:<resource>:<action>" strings as Thunder issues them, matching
// rbac.Permission.Scope() on the service side.
func ScopesExcept(exclude ...string) []string {
	kept := make([]string, 0, len(AllScopes()))
	for _, s := range AllScopes() {
		if !slices.Contains(exclude, s) {
			kept = append(kept, s)
		}
	}
	return kept
}

// FetchTokenWithScopes obtains a client_credentials token carrying exactly the
// given scopes. A nil or empty slice requests no scopes, yielding an unscoped
// token — used to prove RBAC enforcement is switched on at all.
func FetchTokenWithScopes(ctx context.Context, cfg *Config, scopes []string) (string, error) {
	return fetchTokenWithRetry(ctx, cfg, strings.Join(scopes, " "))
}

// TokenScopes decodes the JWT payload (without verifying the signature — the
// server does that) and returns the scope claim as a set. Used to verify the
// IDP honoured a scope-reduction request before relying on it.
func TokenScopes(token string) (map[string]struct{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a JWT: expected 3 dot-separated parts, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}

	var claims struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal JWT payload: %w", err)
	}

	set := make(map[string]struct{})
	for _, s := range strings.Fields(claims.Scope) {
		set[s] = struct{}{}
	}
	return set, nil
}

// TokenAudiences decodes the JWT audience claim without verifying the
// signature. JWT permits aud to be either one string or an array. Security
// tests use this only to explain a server-observed 401; the server remains the
// authority that verifies the token.
func TokenAudiences(token string) ([]string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a JWT: expected 3 dot-separated parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Audience json.RawMessage `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal JWT payload: %w", err)
	}
	if len(claims.Audience) == 0 || string(claims.Audience) == "null" {
		return nil, nil
	}
	var audiences []string
	if err := json.Unmarshal(claims.Audience, &audiences); err == nil {
		return audiences, nil
	}
	var audience string
	if err := json.Unmarshal(claims.Audience, &audience); err != nil {
		return nil, fmt.Errorf("aud claim is neither a string nor a string array: %w", err)
	}
	return []string{audience}, nil
}

// NewAMPClientWithToken builds a client around a caller-supplied token instead
// of fetching a full-privilege one. Pair with FetchTokenWithScopes to drive a
// route with a deliberately incomplete scope set.
func NewAMPClientWithToken(cfg *Config, token string) *AMPClient {
	return &AMPClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    cfg.AMPBaseURL,
		token:      token,
		cfg:        cfg,
	}
}
