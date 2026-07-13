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

package main

import (
	"path/filepath"
	"testing"
)

// TestExtractRoutesAgainstRealSource proves the extractor resolves every route
// registrar call in the live api/ package without a hard-fail. If a new route
// uses a pattern the folder cannot resolve, this fails loudly.
func TestExtractRoutesAgainstRealSource(t *testing.T) {
	permFile, err := loadFile(filepath.Join("..", "..", "rbac", "permissions.go"))
	if err != nil {
		t.Fatalf("load rbac permissions: %v", err)
	}
	perms, err := parsePermissions(permFile)
	if err != nil {
		t.Fatalf("parsePermissions: %v", err)
	}
	if len(perms) < 100 {
		t.Fatalf("expected 100+ permissions parsed, got %d", len(perms))
	}

	files, err := loadPackageFiles(filepath.Join("..", "..", "api"))
	if err != nil {
		t.Fatalf("load api package: %v", err)
	}
	routes, err := extractRoutes(files, perms)
	if err != nil {
		t.Fatalf("extractRoutes against real source hard-failed: %v", err)
	}

	// Sanity floor only — the exact total drifts as routes are added/removed on
	// main, and the committed manifest (checked by gen-gateway-scopes-check) is
	// the real drift guard. Here we just prove the extractor resolved a full,
	// well-formed route set without a hard-fail.
	if len(routes) < 200 {
		t.Fatalf("expected 200+ routes, got %d — the extractor likely missed registrations", len(routes))
	}

	var scoped, anyScoped, jwtOnly int
	for _, r := range routes {
		if r.Method == "" || r.Path == "" {
			t.Errorf("route with empty method/path: %#v", r)
		}
		switch r.Auth {
		case "scopes":
			scoped++
			if len(r.RequiredScopes) != 1 {
				t.Errorf("scopes route must carry exactly one scope: %#v", r)
			}
		case "any-scopes":
			anyScoped++
			if len(r.RequiredScopes) < 2 {
				t.Errorf("any-scopes route should carry 2+ scopes: %#v", r)
			}
		case "jwt-only":
			jwtOnly++
			if r.RequiredScopes != nil {
				t.Errorf("jwt-only route must carry no scopes: %#v", r)
			}
		default:
			t.Errorf("unexpected auth kind %q: %#v", r.Auth, r)
		}
	}
	if scoped+anyScoped+jwtOnly != len(routes) {
		t.Errorf("auth breakdown does not sum to total: scopes:%d any-scopes:%d jwt-only:%d != %d", scoped, anyScoped, jwtOnly, len(routes))
	}
	if scoped == 0 || jwtOnly == 0 {
		t.Errorf("expected a mix of scoped and jwt-only routes, got scopes:%d jwt-only:%d", scoped, jwtOnly)
	}
}
