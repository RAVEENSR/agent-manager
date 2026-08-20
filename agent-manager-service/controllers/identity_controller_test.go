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

// Regression tests for AM-CWE862 (the permission-mutation endpoints skipped
// the predefined-role guard that UpdateRole/DeleteRole enforce): a caller
// holding only the RoleUpdate scope could silently rewrite a predefined,
// high-privilege role's permission set, because AddRolePermissions/
// RemoveRolePermissions checked role ownership but never checked whether the
// role was predefined.
//
// A predefined role's permission set is a fixed system definition — nobody,
// admin included, edits it through the API; a different bundle means a new
// custom role. So AddRolePermissions/RemoveRolePermissions now reject a
// predefined role unconditionally, exactly like UpdateRole/DeleteRole already
// did, with no bypass.
//
// Assigning a *user* to a role (including a predefined one) is a separate,
// intentional capability of RoleUpdate — deciding who holds a role is role
// membership data, not the role's definition — so AddRoleAssignees/
// RemoveRoleAssignees are deliberately unrestricted by role name and are
// pinned here to stay that way.
package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
	"github.com/wso2/agent-manager/agent-manager-service/clients/clientmocks"
	"github.com/wso2/agent-manager/agent-manager-service/clients/thundersvc"
	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/orgctx"
)

// roleRequest builds a request carrying the org context, roleID path value,
// and a no-op audit recorder every identityController role-mutation handler
// needs (the write handlers refuse to proceed if no recorder is installed).
func roleRequest(method, url, body string) *http.Request {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.SetPathValue("roleID", "role-under-test")
	ctx := middleware.WithResolvedOrg(req.Context(), orgctx.ResolvedOrg{OUID: "ou-1", OuHandle: "acme"})
	ctx = audit.WithRecorder(ctx, audit.NewNoopRecorder())
	return req.WithContext(ctx)
}

func identityClientReturning(role *thundersvc.ThunderRole) *clientmocks.IdentityClientMock {
	return &clientmocks.IdentityClientMock{
		GetRoleFunc: func(_ context.Context, _ string) (*thundersvc.ThunderRole, error) {
			return role, nil
		},
	}
}

func predefinedRole() *thundersvc.ThunderRole {
	return &thundersvc.ThunderRole{ID: "role-under-test", OuID: "ou-1", Name: "Agent Manager Admin"}
}

func customRole() *thundersvc.ThunderRole {
	return &thundersvc.ThunderRole{ID: "role-under-test", OuID: "ou-1", Name: "custom-delegated-role"}
}

func TestAddRolePermissions_PredefinedRoleRejected(t *testing.T) {
	client := identityClientReturning(predefinedRole())
	// AddRolePermissionsFunc is left nil: reaching Thunder would panic, proving
	// the guard returned before any mutation was attempted.
	ctrl := NewIdentityController(client)

	req := roleRequest(http.MethodPost, "/orgs/acme/identities/roles/role-under-test/permissions/add",
		`{"resourceServerId":"amp-resource-server","permissions":["amp:org:view"]}`)
	w := httptest.NewRecorder()

	ctrl.AddRolePermissions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddRolePermissions_CustomRoleAllowed(t *testing.T) {
	called := false
	client := identityClientReturning(customRole())
	client.AddRolePermissionsFunc = func(_ context.Context, roleID string, req thundersvc.RolePermissionRequest) error {
		called = true
		assert.Equal(t, "role-under-test", roleID)
		return nil
	}
	ctrl := NewIdentityController(client)

	req := roleRequest(http.MethodPost, "/orgs/acme/identities/roles/role-under-test/permissions/add",
		`{"resourceServerId":"amp-resource-server","permissions":["amp:org:view"]}`)
	w := httptest.NewRecorder()

	ctrl.AddRolePermissions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "a genuinely custom role must still be reachable")
}

func TestRemoveRolePermissions_PredefinedRoleRejected(t *testing.T) {
	client := identityClientReturning(predefinedRole())
	ctrl := NewIdentityController(client)

	req := roleRequest(http.MethodPost, "/orgs/acme/identities/roles/role-under-test/permissions/remove",
		`{"resourceServerId":"amp-resource-server","permissions":["amp:org:view"]}`)
	w := httptest.NewRecorder()

	ctrl.RemoveRolePermissions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRemoveRolePermissions_CustomRoleAllowed(t *testing.T) {
	called := false
	client := identityClientReturning(customRole())
	client.RemoveRolePermissionsFunc = func(_ context.Context, roleID string, req thundersvc.RolePermissionRequest) error {
		called = true
		return nil
	}
	ctrl := NewIdentityController(client)

	req := roleRequest(http.MethodPost, "/orgs/acme/identities/roles/role-under-test/permissions/remove",
		`{"resourceServerId":"amp-resource-server","permissions":["amp:org:view"]}`)
	w := httptest.NewRecorder()

	ctrl.RemoveRolePermissions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called)
}

// TestAddRoleAssignees_PredefinedRoleAllowed pins the deliberate distinction
// from the permission-mutation endpoints above: adding a user as an assignee
// of a predefined role is role-membership data, which RoleUpdate is meant to
// cover, so it must NOT be blocked the way editing the role's permissions is.
func TestAddRoleAssignees_PredefinedRoleAllowed(t *testing.T) {
	called := false
	client := identityClientReturning(predefinedRole())
	client.AddRoleAssigneesFunc = func(_ context.Context, roleID string, req thundersvc.RoleAssignmentsRequest) error {
		called = true
		return nil
	}
	ctrl := NewIdentityController(client)

	req := roleRequest(http.MethodPost, "/orgs/acme/identities/roles/role-under-test/assignees/add",
		`{"userIds":["some-user"]}`)
	w := httptest.NewRecorder()

	ctrl.AddRoleAssignees(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "assigning a user to a predefined role is role-membership data, not a definition change")
}

func TestAddRoleAssignees_CustomRoleAllowed(t *testing.T) {
	called := false
	client := identityClientReturning(customRole())
	client.AddRoleAssigneesFunc = func(_ context.Context, roleID string, req thundersvc.RoleAssignmentsRequest) error {
		called = true
		return nil
	}
	ctrl := NewIdentityController(client)

	req := roleRequest(http.MethodPost, "/orgs/acme/identities/roles/role-under-test/assignees/add",
		`{"userIds":["some-user"]}`)
	w := httptest.NewRecorder()

	ctrl.AddRoleAssignees(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "assigning to a genuinely custom role must still work")
}

func TestRemoveRoleAssignees_PredefinedRoleAllowed(t *testing.T) {
	called := false
	client := identityClientReturning(predefinedRole())
	client.RemoveRoleAssigneesFunc = func(_ context.Context, roleID string, req thundersvc.RoleAssignmentsRequest) error {
		called = true
		return nil
	}
	ctrl := NewIdentityController(client)

	req := roleRequest(http.MethodPost, "/orgs/acme/identities/roles/role-under-test/assignees/remove",
		`{"userIds":["some-user"]}`)
	w := httptest.NewRecorder()

	ctrl.RemoveRoleAssignees(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "removing a user from a predefined role is role-membership data, not a definition change")
}

func TestRemoveRoleAssignees_CustomRoleAllowed(t *testing.T) {
	called := false
	client := identityClientReturning(customRole())
	client.RemoveRoleAssigneesFunc = func(_ context.Context, roleID string, req thundersvc.RoleAssignmentsRequest) error {
		called = true
		return nil
	}
	ctrl := NewIdentityController(client)

	req := roleRequest(http.MethodPost, "/orgs/acme/identities/roles/role-under-test/assignees/remove",
		`{"userIds":["some-user"]}`)
	w := httptest.NewRecorder()

	ctrl.RemoveRoleAssignees(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called)
}
