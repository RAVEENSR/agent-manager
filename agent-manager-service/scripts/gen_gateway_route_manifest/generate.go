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

// Command gen_gateway_route_manifest generates the API Platform Gateway
// route-to-scope manifest from the agent-manager-service HTTP route registrars.
//
// It is the single source of truth bridge: the Go route registrars in api/*.go
// declare, per route, the RBAC permission required, and this tool projects that
// into a declarative manifest the wso2-agent-manager Helm chart renders into the
// RestApi CR's per-operation jwt-auth policies. See the design doc
// (docs/superpowers/specs/20260710ampapigatewaydesign.md, section 3).
package main

import (
	"fmt"
	"go/ast"
	"sort"
	"strconv"
	"strings"
)

// Route is one generated gateway operation: an HTTP method + path (relative to
// the /api/v1 context) and the scope enforcement the gateway should apply.
type Route struct {
	Method         string
	Path           string
	Auth           string // "scopes" | "any-scopes" | "jwt-only"
	RequiredScopes []string
}

// resourceServerPrefix mirrors rbac.ResourceServer; scopes are "<prefix>:<permission>".
const resourceServerPrefix = "amp"

// validMethods are the HTTP methods accepted in a Go 1.22 mux route pattern.
var validMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

// registrarAuth maps each RouteRegistrar method to how the gateway should treat
// the route. Any rr.HandleFunc* method not listed here is a hard error, so a new
// registrar variant cannot silently slip routes past the generator.
//
// Values: "authz" = single required scope (ANY-of over one == exact);
// "any-authz" = ANY-of over a list; "jwt-only" = authenticate but do not enforce
// scopes at the gateway (amp-api remains the authority for dynamic / root-OU /
// validation-only routes).
var registrarAuth = map[string]string{
	"HandleFuncWithValidationAndAuthz":            "authz",
	"HandleFuncWithValidationAndAnyAuthz":         "any-authz",
	"HandleFuncWithValidation":                    "jwt-only",
	"HandleFuncWithValidationAndAuthzAllowRootOU": "jwt-only",
	"HandleFuncWithValidationAndDynamicAuthz":     "jwt-only",
}

// parsePermissions reads an rbac permissions source file and returns a map from
// the Go constant name (e.g. "MonitorScoreRead") to its full scope string
// (e.g. "amp:monitor:score-read"), matching rbac.Permission.Scope().
func parsePermissions(file *ast.File) (map[string]string, error) {
	perms := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			typeName, ok := vs.Type.(*ast.Ident)
			if !ok || typeName.Name != "Permission" || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return nil, fmt.Errorf("permission %s: %w", vs.Names[0].Name, err)
			}
			perms[vs.Names[0].Name] = resourceServerPrefix + ":" + value
		}
	}
	return perms, nil
}

// extractRoutes walks the given files for RouteRegistrar (rr.HandleFunc*) calls
// and returns one Route per registration. It hard-fails on any pattern or
// permission it cannot statically resolve, so drift surfaces as a build error.
func extractRoutes(files []*ast.File, perms map[string]string) ([]Route, error) {
	var routes []Route
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			fnRoutes, err := routesInFunc(fn, perms)
			if err != nil {
				return nil, err
			}
			routes = append(routes, fnRoutes...)
		}
	}
	return routes, nil
}

// routesInFunc scans a single function body top-to-bottom, tracking local
// string variables so patterns built from base + suffix concatenations resolve.
func routesInFunc(fn *ast.FuncDecl, perms map[string]string) ([]Route, error) {
	locals := map[string]string{}
	var routes []Route
	for _, stmt := range fn.Body.List {
		switch s := stmt.(type) {
		case *ast.AssignStmt:
			recordLocals(s, locals)
		case *ast.ExprStmt:
			call, ok := s.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			route, matched, err := routeFromCall(call, locals, perms)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fn.Name.Name, err)
			}
			if matched {
				routes = append(routes, route)
			}
		}
	}
	return routes, nil
}

// recordLocals stores any assignment whose RHS resolves to a string literal
// expression, so later patterns referencing the variable can be folded.
func recordLocals(assign *ast.AssignStmt, locals map[string]string) {
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return
	}
	name, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}
	if v, err := resolveString(assign.Rhs[0], locals); err == nil {
		locals[name.Name] = v
	}
}

// routeFromCall turns an rr.HandleFunc* call into a Route. matched is false for
// any call that is not a RouteRegistrar registration.
func routeFromCall(call *ast.CallExpr, locals, perms map[string]string) (Route, bool, error) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return Route{}, false, nil
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok || recv.Name != "rr" {
		return Route{}, false, nil
	}
	kind, known := registrarAuth[sel.Sel.Name]
	if !known {
		if strings.HasPrefix(sel.Sel.Name, "HandleFunc") {
			return Route{}, false, fmt.Errorf("unknown RouteRegistrar method %q; add it to registrarAuth", sel.Sel.Name)
		}
		return Route{}, false, nil
	}
	if len(call.Args) == 0 {
		return Route{}, false, fmt.Errorf("%s: no arguments", sel.Sel.Name)
	}
	pattern, err := resolveString(call.Args[0], locals)
	if err != nil {
		return Route{}, false, fmt.Errorf("%s: cannot resolve route pattern: %w", sel.Sel.Name, err)
	}
	method, path, err := splitPattern(pattern)
	if err != nil {
		return Route{}, false, err
	}

	route := Route{Method: method, Path: path, Auth: "jwt-only", RequiredScopes: nil}
	switch kind {
	case "authz":
		scope, err := resolvePermission(call.Args[1], perms)
		if err != nil {
			return Route{}, false, fmt.Errorf("%s %s: %w", method, path, err)
		}
		route.Auth = "scopes"
		route.RequiredScopes = []string{scope}
	case "any-authz":
		var scopes []string
		for _, arg := range call.Args[2:] {
			scope, err := resolvePermission(arg, perms)
			if err != nil {
				return Route{}, false, fmt.Errorf("%s %s: %w", method, path, err)
			}
			scopes = append(scopes, scope)
		}
		route.Auth = "any-scopes"
		route.RequiredScopes = scopes
	}
	return route, true, nil
}

// resolveString folds a string-typed expression to its literal value, handling
// string literals, local variables, "+" concatenation, and the api.route(method,
// path) helper (which yields "method path").
func resolveString(expr ast.Expr, locals map[string]string) (string, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return strconv.Unquote(e.Value)
	case *ast.Ident:
		if v, ok := locals[e.Name]; ok {
			return v, nil
		}
		return "", fmt.Errorf("unresolved identifier %q", e.Name)
	case *ast.BinaryExpr:
		left, err := resolveString(e.X, locals)
		if err != nil {
			return "", err
		}
		right, err := resolveString(e.Y, locals)
		if err != nil {
			return "", err
		}
		return left + right, nil
	case *ast.CallExpr:
		fn, ok := e.Fun.(*ast.Ident)
		if ok && fn.Name == "route" && len(e.Args) == 2 {
			method, err := resolveString(e.Args[0], locals)
			if err != nil {
				return "", err
			}
			path, err := resolveString(e.Args[1], locals)
			if err != nil {
				return "", err
			}
			return method + " " + path, nil
		}
		return "", fmt.Errorf("unsupported call in pattern expression")
	default:
		return "", fmt.Errorf("unsupported expression of type %T in pattern", expr)
	}
}

// resolvePermission resolves an rbac.<Name> selector to its scope string.
func resolvePermission(expr ast.Expr, perms map[string]string) (string, error) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", fmt.Errorf("permission argument is not an rbac.<Permission> selector")
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "rbac" {
		return "", fmt.Errorf("permission argument is not from the rbac package")
	}
	scope, ok := perms[sel.Sel.Name]
	if !ok {
		return "", fmt.Errorf("unknown rbac permission %q", sel.Sel.Name)
	}
	return scope, nil
}

// splitPattern splits a Go 1.22 mux pattern ("GET /path/{id}") into method and
// path, validating the method and rejecting greedy "{x...}" wildcards that have
// no 1:1 gateway path-template equivalent.
func splitPattern(pattern string) (method, path string, err error) {
	parts := strings.SplitN(strings.TrimSpace(pattern), " ", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("route pattern %q is missing a method or path", pattern)
	}
	method, path = parts[0], parts[1]
	if !validMethods[method] {
		return "", "", fmt.Errorf("route pattern %q has unsupported method %q", pattern, method)
	}
	if !strings.HasPrefix(path, "/") {
		return "", "", fmt.Errorf("route pattern %q path must start with '/'", pattern)
	}
	if strings.Contains(path, "...") {
		return "", "", fmt.Errorf("route pattern %q uses a greedy {x...} wildcard with no gateway equivalent", pattern)
	}
	return method, path, nil
}

// renderManifest emits the deterministic, sorted YAML manifest.
func renderManifest(routes []Route) ([]byte, error) {
	sorted := make([]Route, len(routes))
	copy(sorted, routes)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		return sorted[i].Method < sorted[j].Method
	})

	var b strings.Builder
	b.WriteString("# Code generated by gen_gateway_route_manifest; DO NOT EDIT.\n")
	b.WriteString("# Regenerate with: make gen-gateway-scopes\n")
	b.WriteString("context: /api/v1\n")
	b.WriteString("routes:\n")
	for _, r := range sorted {
		fmt.Fprintf(&b, "  - method: %s\n", r.Method)
		fmt.Fprintf(&b, "    path: %s\n", strconv.Quote(r.Path))
		fmt.Fprintf(&b, "    auth: %s\n", r.Auth)
		if len(r.RequiredScopes) > 0 {
			quoted := make([]string, len(r.RequiredScopes))
			for i, s := range r.RequiredScopes {
				quoted[i] = strconv.Quote(s)
			}
			fmt.Fprintf(&b, "    requiredScopes: [%s]\n", strings.Join(quoted, ", "))
		}
	}
	return []byte(b.String()), nil
}
