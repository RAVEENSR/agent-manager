// Copyright (c) 2025, WSO2 LLC. (https://www.wso2.com).
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
	"net/http"
	"regexp"
	"strings"

	"github.com/wso2/agent-manager/agent-manager-service/rbac"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

const DefaultOrgName = "default"

const orgNamePlaceholder = "{" + utils.PathParamOrgName + "}"

// RouteRegistrar wraps an http.ServeMux and an OrgResolver to provide route
// registration helpers that automatically apply org validation + resolution on
// any pattern containing {orgName}.
type RouteRegistrar struct {
	mux         *http.ServeMux
	orgResolver OrgResolver
}

// NewRouteRegistrar creates a RouteRegistrar backed by the given mux and resolver.
func NewRouteRegistrar(mux *http.ServeMux, resolver OrgResolver) *RouteRegistrar {
	return &RouteRegistrar{mux: mux, orgResolver: resolver}
}

func (rr *RouteRegistrar) HandleFuncWithValidation(pattern string, handler http.HandlerFunc) {
	params := extractPathParams(pattern)
	if len(params) > 0 {
		handler = WithPathParamValidation(handler, params...)
	}
	if strings.Contains(pattern, orgNamePlaceholder) {
		handler = RequireOrgMatch(rr.orgResolver)(handler)
	}
	rr.mux.HandleFunc(pattern, handler)
}

func (rr *RouteRegistrar) HandleFuncWithValidationAndAuthz(pattern string, perm rbac.Permission, handler http.HandlerFunc) {
	params := extractPathParams(pattern)
	if len(params) > 0 {
		handler = WithPathParamValidation(handler, params...)
	}
	handler = RequirePermission(perm)(handler)
	if strings.Contains(pattern, orgNamePlaceholder) {
		handler = RequireOrgMatch(rr.orgResolver)(handler)
	}
	rr.mux.HandleFunc(pattern, handler)
}

// HandleFuncWithValidationAndAuthzAllowRootOU is identical to
// HandleFuncWithValidationAndAuthz except it also bypasses the scope check for
// a client-credentials token issued to the configured root/admin OU. Org
// resolution is unchanged from the normal path: under the token-trust model
// the org always comes from the token, so this does NOT let a root-OU token
// act on a different org named in the path — see RequireOrgMatchAllowRootOU /
// RequirePermissionAllowRootOU. Use only for system-client bootstrap endpoints.
func (rr *RouteRegistrar) HandleFuncWithValidationAndAuthzAllowRootOU(pattern string, perm rbac.Permission, handler http.HandlerFunc) {
	params := extractPathParams(pattern)
	if len(params) > 0 {
		handler = WithPathParamValidation(handler, params...)
	}
	handler = RequirePermissionAllowRootOU(perm)(handler)
	if strings.Contains(pattern, orgNamePlaceholder) {
		handler = RequireOrgMatchAllowRootOU(rr.orgResolver)(handler)
	}
	rr.mux.HandleFunc(pattern, handler)
}

func (rr *RouteRegistrar) HandleFuncWithValidationAndAnyAuthz(pattern string, handler http.HandlerFunc, perms ...rbac.Permission) {
	params := extractPathParams(pattern)
	if len(params) > 0 {
		handler = WithPathParamValidation(handler, params...)
	}
	handler = RequireAnyPermission(perms...)(handler)
	if strings.Contains(pattern, orgNamePlaceholder) {
		handler = RequireOrgMatch(rr.orgResolver)(handler)
	}
	rr.mux.HandleFunc(pattern, handler)
}

func (rr *RouteRegistrar) HandleFuncWithValidationAndDynamicAuthz(pattern string, resolver PermissionResolver, handler http.HandlerFunc) {
	params := extractPathParams(pattern)
	if len(params) > 0 {
		handler = WithPathParamValidation(handler, params...)
	}
	handler = RequireDynamicPermission(resolver)(handler)
	if strings.Contains(pattern, orgNamePlaceholder) {
		handler = RequireOrgMatch(rr.orgResolver)(handler)
	}
	rr.mux.HandleFunc(pattern, handler)
}

// WithPathParamValidation wraps a handler and validates required path parameters
// This runs after route matching, so r.PathValue() works correctly
func WithPathParamValidation(handler http.HandlerFunc, requiredParams ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Validate each required parameter
		for _, paramName := range requiredParams {
			value := r.PathValue(paramName)
			if strings.TrimSpace(value) == "" {
				utils.WriteErrorResponse(w, http.StatusBadRequest, "Missing required path parameter: "+paramName)
				return
			}
		}

		// All validations passed, call the original handler
		handler(w, r)
	}
}

// extractPathParams extracts parameter names from a route pattern
// Example: "GET /orgs/{orgName}/projects/{projName}" -> ["orgName", "projName"]
func extractPathParams(pattern string) []string {
	re := regexp.MustCompile(`\{([^}]+)\}`)
	matches := re.FindAllStringSubmatch(pattern, -1)

	params := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			paramName := strings.TrimSpace(match[1])
			params = append(params, paramName)
		}
	}

	return params
}
