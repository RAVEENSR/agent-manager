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

package tools

import (
	"context"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/auth"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wso2/agent-manager/agent-manager-service/config"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/jwtassertion"
	"github.com/wso2/agent-manager/agent-manager-service/rbac"
)

// methodCallTool is the MCP method name for tool invocations. The SDK's own
// constant is unexported.
const methodCallTool = "tools/call"

// TokenInfoOUIDKey is the auth.TokenInfo.Extra key under which the MCP token
// verifier (mcp/tokeninfo.go) records the organization of the token that made
// the current request. authzMiddleware compares it against the session's
// organization so a per-request token cannot drive a tool against a different
// org than the one whose identity established the session.
const TokenInfoOUIDKey = "amp:ou-id"

// toolRegistry records the rbac permissions each registered tool requires.
// authzMiddleware enforces it fail-closed: a tool with no entry — one
// registered by bypassing addTool — is always denied.
type toolRegistry struct {
	permissions map[string][]rbac.Permission
}

func newToolRegistry() *toolRegistry {
	return &toolRegistry{permissions: make(map[string][]rbac.Permission)}
}

// addTool is the only sanctioned way to register an MCP tool. It requires the
// permissions the caller's token must hold (ALL semantics), records them for
// authzMiddleware, and wires the standard logging wrapper. Panics when no
// permission is declared: that is a registration bug, caught at startup and
// by every test run, not a runtime condition.
func addTool[T any](reg *toolRegistry, server *gomcp.Server, tool *gomcp.Tool,
	handler func(context.Context, *gomcp.CallToolRequest, T) (*gomcp.CallToolResult, any, error),
	perms ...rbac.Permission,
) {
	if len(perms) == 0 {
		panic(fmt.Sprintf("mcp tool %q registered without permissions", tool.Name))
	}
	reg.permissions[tool.Name] = perms
	gomcp.AddTool(server, tool, withToolLogging(tool.Name, handler))
}

// authzMiddleware returns a server middleware that authorizes every tools/call
// against the registry, mirroring middleware.RequirePermission semantics:
// RBAC_ENABLED=false skips the scope check (zero-downtime rollout switch),
// while the unknown-tool denial applies regardless. Denials are returned as
// IsError tool results so MCP clients surface an actionable message instead
// of a protocol error.
func (reg *toolRegistry) authzMiddleware() gomcp.Middleware {
	return func(next gomcp.MethodHandler) gomcp.MethodHandler {
		return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
			if method != methodCallTool {
				return next(ctx, method, req)
			}
			call, ok := req.(*gomcp.CallToolRequest)
			if !ok {
				return denyResult(fmt.Sprintf("unexpected %s request type", methodCallTool)), nil
			}
			perms, registered := reg.permissions[call.Params.Name]
			if !registered {
				return denyResult(fmt.Sprintf("tool %q has no registered permissions", call.Params.Name)), nil
			}
			// Organization-consistency guard: tool handlers resolve the org from
			// the session/initialize context (resolveOUID) while scopes are taken
			// from the per-request token. Reject when the per-request token targets
			// a different org than the session, so scopes granted in one org cannot
			// authorize actions against another. This is an identity-integrity check
			// like the SDK's sub-based session-hijack guard, so it applies
			// regardless of RBAC_ENABLED. Skipped when there is no per-request
			// TokenInfo (in-memory transports have no HTTP layer).
			if call.Extra != nil && call.Extra.TokenInfo != nil {
				if !sessionOrgMatchesRequest(ctx, call.Extra.TokenInfo) {
					return denyResult("organization mismatch: request token is not scoped to the session organization"), nil
				}
			}
			if !config.GetConfig().RBACEnabled {
				return next(ctx, method, req)
			}
			hasScope := scopeChecker(ctx, call)
			for _, perm := range perms {
				if !hasScope(perm.Scope()) {
					return denyResult(fmt.Sprintf("insufficient permissions: this tool requires the %s scope", perm.Scope())), nil
				}
			}
			return next(ctx, method, req)
		}
	}
}

// scopeChecker returns a function that reports whether a given scope is
// present for the current request. It prefers the per-request scopes the SDK
// attaches via call.Extra.TokenInfo (populated by claimsTokenVerifier through
// auth.RequireBearerToken — see mcp/tokeninfo.go and mcp/setup.go) since those
// reflect the token that made this specific HTTP request. When Extra.TokenInfo
// is absent — in-memory transports and tests have no HTTP layer, so it's
// always nil there — it falls back to the scopes jwtassertion put on the
// session context.
func scopeChecker(ctx context.Context, call *gomcp.CallToolRequest) func(scope string) bool {
	if call.Extra != nil && call.Extra.TokenInfo != nil {
		scopes := call.Extra.TokenInfo.Scopes
		return func(scope string) bool {
			return slices.Contains(scopes, scope)
		}
	}
	return func(scope string) bool {
		return jwtassertion.HasAllScopes(ctx, []string{scope})
	}
}

// sessionOrgMatchesRequest reports whether the organization on the per-request
// token (recorded under TokenInfoOUIDKey by claimsTokenVerifier) equals the
// organization of the session's claims on ctx. A session with no claims yields
// an empty org, which only matches a request that likewise carries no org.
func sessionOrgMatchesRequest(ctx context.Context, info *auth.TokenInfo) bool {
	var sessionOUID string
	if claims := jwtassertion.GetTokenClaims(ctx); claims != nil {
		sessionOUID = claims.OuId
	}
	requestOUID, _ := info.Extra[TokenInfoOUIDKey].(string)
	return requestOUID == sessionOUID
}

func denyResult(message string) *gomcp.CallToolResult {
	return &gomcp.CallToolResult{
		IsError: true,
		Content: []gomcp.Content{&gomcp.TextContent{Text: message}},
	}
}
