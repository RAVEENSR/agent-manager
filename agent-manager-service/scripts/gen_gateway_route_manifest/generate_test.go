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
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

// parseSrc is a test helper that parses a Go source snippet into an *ast.File.
func parseSrc(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "src.go", src, 0)
	if err != nil {
		t.Fatalf("failed to parse test source: %v", err)
	}
	return f
}

func TestParsePermissionsExtractsScopeStrings(t *testing.T) {
	src := `package rbac
const ResourceServer = "amp"
type Permission string
const (
	OrgView       Permission = "org:view"
	ProjectCreate Permission = "project:create"
)
const MonitorScoreRead Permission = "monitor:score-read"
`
	perms, err := parsePermissions(parseSrc(t, src))
	if err != nil {
		t.Fatalf("parsePermissions: unexpected error: %v", err)
	}
	want := map[string]string{
		"OrgView":          "amp:org:view",
		"ProjectCreate":    "amp:project:create",
		"MonitorScoreRead": "amp:monitor:score-read",
	}
	if !reflect.DeepEqual(perms, want) {
		t.Errorf("parsePermissions =\n %#v\nwant\n %#v", perms, want)
	}
}

// stdPerms is a fixed permission map used by extractRoutes tests.
var stdPerms = map[string]string{
	"MonitorScoreRead": "amp:monitor:score-read",
	"MonitorRead":      "amp:monitor:read",
	"OrgInviteMember":  "amp:org:invite-member",
	"OrgRemoveMember":  "amp:org:remove-member",
}

func routesFrom(t *testing.T, body string) ([]Route, error) {
	t.Helper()
	src := "package api\n" +
		"func route(method, path string) string { return method + \" \" + path }\n" +
		"func reg(rr *middleware.RouteRegistrar) {\n" + body + "\n}\n"
	return extractRoutes([]*ast.File{parseSrc(t, src)}, stdPerms)
}

func TestExtractRoutesSingleAuthzLiteral(t *testing.T) {
	routes, err := routesFrom(t, `rr.HandleFuncWithValidationAndAuthz("GET /orgs/{orgName}/monitors", rbac.MonitorRead, ctrl.List)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Route{{Method: "GET", Path: "/orgs/{orgName}/monitors", Auth: "scopes", RequiredScopes: []string{"amp:monitor:read"}}}
	if !reflect.DeepEqual(routes, want) {
		t.Errorf("got %#v want %#v", routes, want)
	}
}

func TestExtractRoutesAnyAuthzMultiScope(t *testing.T) {
	routes, err := routesFrom(t, `rr.HandleFuncWithValidationAndAnyAuthz("GET /orgs/{orgName}/identities/users", ctrl.ListUsers, rbac.OrgInviteMember, rbac.OrgRemoveMember)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Route{{Method: "GET", Path: "/orgs/{orgName}/identities/users", Auth: "any-scopes", RequiredScopes: []string{"amp:org:invite-member", "amp:org:remove-member"}}}
	if !reflect.DeepEqual(routes, want) {
		t.Errorf("got %#v want %#v", routes, want)
	}
}

func TestExtractRoutesPlainValidationIsJWTOnly(t *testing.T) {
	routes, err := routesFrom(t, `rr.HandleFuncWithValidation("GET /orgs/{orgName}/agent-build-options", ctrl.Get)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Route{{Method: "GET", Path: "/orgs/{orgName}/agent-build-options", Auth: "jwt-only", RequiredScopes: nil}}
	if !reflect.DeepEqual(routes, want) {
		t.Errorf("got %#v want %#v", routes, want)
	}
}

func TestExtractRoutesAllowRootOUAndDynamicAreJWTOnly(t *testing.T) {
	routes, err := routesFrom(t, `
	rr.HandleFuncWithValidationAndAuthzAllowRootOU("POST /orgs/{orgName}/gateways/register", rbac.MonitorRead, ctrl.Reg)
	rr.HandleFuncWithValidationAndDynamicAuthz("GET /orgs/{orgName}/x", resolver, ctrl.X)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range routes {
		if r.Auth != "jwt-only" || r.RequiredScopes != nil {
			t.Errorf("expected jwt-only with no scopes, got %#v", r)
		}
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
}

func TestExtractRoutesFoldsLocalsConcatAndRouteHelper(t *testing.T) {
	body := `
	agentBase := "/orgs/{orgName}/projects/{projName}/agents/{agentName}"
	monitorBase := agentBase + "/monitors/{monitorName}"
	rr.HandleFuncWithValidationAndAuthz(route("GET", monitorBase+"/scores"), rbac.MonitorScoreRead, ctrl.GetScores)`
	routes, err := routesFrom(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Route{{
		Method:         "GET",
		Path:           "/orgs/{orgName}/projects/{projName}/agents/{agentName}/monitors/{monitorName}/scores",
		Auth:           "scopes",
		RequiredScopes: []string{"amp:monitor:score-read"},
	}}
	if !reflect.DeepEqual(routes, want) {
		t.Errorf("got %#v want %#v", routes, want)
	}
}

func TestExtractRoutesHardFailsOnUnresolvableIdent(t *testing.T) {
	_, err := routesFrom(t, `rr.HandleFuncWithValidationAndAuthz(somethingDynamic, rbac.MonitorRead, ctrl.X)`)
	if err == nil {
		t.Fatal("expected hard-fail on unresolvable pattern, got nil")
	}
}

func TestExtractRoutesHardFailsOnUnknownPermission(t *testing.T) {
	_, err := routesFrom(t, `rr.HandleFuncWithValidationAndAuthz("GET /x", rbac.NotARealPermission, ctrl.X)`)
	if err == nil {
		t.Fatal("expected hard-fail on unknown rbac permission, got nil")
	}
}

func TestExtractRoutesHardFailsOnGreedyWildcard(t *testing.T) {
	_, err := routesFrom(t, `rr.HandleFuncWithValidationAndAuthz("GET /files/{path...}", rbac.MonitorRead, ctrl.X)`)
	if err == nil {
		t.Fatal("expected hard-fail on greedy {path...} wildcard, got nil")
	}
}

func TestRenderManifestIsSortedAndDeterministic(t *testing.T) {
	routes := []Route{
		{Method: "POST", Path: "/orgs/{orgName}/monitors", Auth: "scopes", RequiredScopes: []string{"amp:monitor:create"}},
		{Method: "GET", Path: "/orgs/{orgName}/monitors", Auth: "scopes", RequiredScopes: []string{"amp:monitor:read"}},
		{Method: "GET", Path: "/publisher/x", Auth: "jwt-only", RequiredScopes: nil},
	}
	out, err := renderManifest(routes)
	if err != nil {
		t.Fatalf("renderManifest: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "context: /api/v1") {
		t.Error("manifest missing context header")
	}
	if !strings.Contains(got, "DO NOT EDIT") {
		t.Error("manifest missing generated-code banner")
	}
	// GET must sort before POST for the same path; jwt-only route omits requiredScopes.
	getIdx := strings.Index(got, "path: \"/orgs/{orgName}/monitors\"\n    auth: scopes\n    requiredScopes: [\"amp:monitor:read\"]")
	postIdx := strings.Index(got, "auth: scopes\n    requiredScopes: [\"amp:monitor:create\"]")
	if getIdx < 0 || postIdx < 0 || getIdx > postIdx {
		t.Errorf("routes not sorted (method within path); output:\n%s", got)
	}
	if strings.Contains(got, "requiredScopes: []") {
		t.Error("jwt-only route should omit requiredScopes, not emit an empty list")
	}
	// deterministic
	out2, _ := renderManifest(routes)
	if string(out2) != got {
		t.Error("renderManifest not deterministic")
	}
}
