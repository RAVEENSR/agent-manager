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

package controllers

import (
	"net/http"

	"github.com/wso2/agent-manager/agent-manager-service/audit"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

// beginAuditOrFail starts a fail-closed audit attempt and, when the trail
// cannot accept it, writes the refusal response itself.
//
// Controllers repeated this block verbatim at every fail-closed site: begin,
// log, 503, return. Centralising it makes the refusal impossible to forget and
// keeps the status and message consistent — a site that logged but returned 500,
// or logged and continued, would be a silent hole in the guarantee.
//
// Returns false when the caller must stop; the response is already written.
func beginAuditOrFail(
	w http.ResponseWriter,
	r *http.Request,
	operation, failureMessage string,
	action audit.Action,
	opts ...audit.Option,
) (*audit.Attempt, bool) {
	ctx := r.Context()

	attempt, err := audit.Begin(ctx, action, opts...)
	if err != nil {
		logger.GetLogger(ctx).Error(operation+": refusing, audit record could not be written",
			"action", string(action), "error", err)
		utils.WriteErrorResponse(w, http.StatusServiceUnavailable, failureMessage)
		return nil, false
	}
	return attempt, true
}

// beginConfigAPIKeyAudit records the intent to change an API key belonging to
// an agent's model or MCP configuration, writing the refusal response itself
// when the trail cannot accept it.
//
// These routes share one permission and one action with every other API-key
// route, so without the owner details a record cannot say which configuration
// the key belonged to. The key value is never passed in.
func beginConfigAPIKeyAudit(
	w http.ResponseWriter,
	r *http.Request,
	operation, failureMessage string,
	action audit.Action,
	ownerType, ouID, projName, agentName, envName, configID, keyName string,
) (*audit.Attempt, bool) {
	return beginAuditOrFail(
		w, r, operation, failureMessage, action,
		audit.Org(ouID),
		audit.ResourceNamed(audit.ResourceAPIKey, configID, keyName),
		audit.Project(projName),
		audit.Environment(envName),
		audit.Detail("ownerType", ownerType),
		audit.Detail("ownerName", agentName),
		audit.Detail("keyName", keyName),
	)
}
