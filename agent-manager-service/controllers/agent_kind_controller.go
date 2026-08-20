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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/wso2/agent-manager/agent-manager-service/middleware"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/models"
	"github.com/wso2/agent-manager/agent-manager-service/services"
	"github.com/wso2/agent-manager/agent-manager-service/spec"
	"github.com/wso2/agent-manager/agent-manager-service/utils"
)

type AgentKindController interface {
	ListKinds(w http.ResponseWriter, r *http.Request)
	GetKind(w http.ResponseWriter, r *http.Request)
	UpdateKind(w http.ResponseWriter, r *http.Request)
	DeleteKind(w http.ResponseWriter, r *http.Request)
	AddVersion(w http.ResponseWriter, r *http.Request)
	ListVersions(w http.ResponseWriter, r *http.Request)
	GetVersion(w http.ResponseWriter, r *http.Request)
	DeleteVersion(w http.ResponseWriter, r *http.Request)
	ListKindAgents(w http.ResponseWriter, r *http.Request)
}

type agentKindController struct {
	kindService services.AgentKindService
}

func NewAgentKindController(kindService services.AgentKindService) AgentKindController {
	return &agentKindController{kindService: kindService}
}

func (c *agentKindController) ListKinds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)

	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		limitStr = strconv.Itoa(utils.DefaultLimit)
	}
	offsetStr := r.URL.Query().Get("offset")
	if offsetStr == "" {
		offsetStr = strconv.Itoa(utils.DefaultOffset)
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < utils.MinLimit || limit > utils.MaxLimit {
		utils.WriteErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid limit parameter: must be between %d and %d", utils.MinLimit, utils.MaxLimit))
		return
	}
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < utils.MinOffset {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid offset parameter: must be >= 0")
		return
	}

	labelFilter, err := utils.ParseLabelSelectors(r.URL.Query()["label"])
	if err != nil {
		log.Debug("ListKinds: invalid label parameter", "error", err)
		utils.WriteValidationErrorResponse(w, err)
		return
	}

	result, err := c.kindService.ListKinds(ctx, ouID, labelFilter, limit, offset)
	if err != nil {
		log.Error("Failed to list agent kinds", "error", err)
		handleCommonErrors(w, err, "Failed to list agent kinds")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, result)
}

func (c *agentKindController) GetKind(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)
	kindName := r.PathValue(utils.PathParamKindName)

	result, err := c.kindService.GetKind(ctx, ouID, kindName)
	if err != nil {
		if errors.Is(err, utils.ErrAgentKindNotFound) {
			// Not being published yet is a normal state for callers probing
			// "does this kind exist," not an error — respond 200 with a null
			// body instead of 404 (result is already nil here).
			utils.WriteSuccessResponse(w, http.StatusOK, result)
			return
		}
		log.Error("Failed to get agent kind", "error", err)
		handleCommonErrors(w, err, "Failed to get agent kind")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, result)
}

func (c *agentKindController) UpdateKind(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)
	kindName := r.PathValue(utils.PathParamKindName)

	var payload spec.UpdateAgentKindRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if payload.Labels != nil {
		if err := utils.ValidateLabels(*payload.Labels); err != nil {
			log.Debug("UpdateKind: invalid labels", "error", err)
			utils.WriteValidationErrorResponse(w, err)
			return
		}
	}

	result, err := c.kindService.UpdateKind(ctx, ouID, kindName, &payload)
	if err != nil {
		log.Error("Failed to update agent kind", "error", err)
		handleCommonErrors(w, err, "Failed to update agent kind")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, result)
}

func (c *agentKindController) DeleteKind(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)
	kindName := r.PathValue(utils.PathParamKindName)

	if err := c.kindService.DeleteKind(ctx, ouID, kindName); err != nil {
		log.Error("Failed to delete agent kind", "error", err)
		handleCommonErrors(w, err, "Failed to delete agent kind")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *agentKindController) AddVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)
	kindName := r.PathValue(utils.PathParamKindName)

	var payload spec.AddAgentKindVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if payload.GetVersion() == "" || payload.GetBuildName() == "" || payload.GetSourceAgentName() == "" || payload.GetSourceProjectName() == "" {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "version, buildName, sourceAgentName, and sourceProjectName are required")
		return
	}
	// The tag is stamped onto every agent created from this version as a label,
	// so reject an unusable one here rather than at agent-creation time.
	if err := utils.ValidateLabelValue(payload.GetVersion(), "version"); err != nil {
		log.Debug("AddVersion: invalid version tag", "error", err)
		utils.WriteValidationErrorResponse(w, err)
		return
	}

	result, err := c.kindService.AddVersion(ctx, ouID, kindName, &payload)
	if err != nil {
		log.Error("Failed to add agent kind version", "error", err)
		handleCommonErrors(w, err, "Failed to add agent kind version")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusCreated, result)
}

func (c *agentKindController) ListVersions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)
	kindName := r.PathValue(utils.PathParamKindName)

	result, err := c.kindService.ListVersions(ctx, ouID, kindName)
	if err != nil {
		if errors.Is(err, utils.ErrAgentKindNotFound) {
			// No kind published yet means no versions — respond 200 with an
			// empty list (not the nil `result`, which would marshal to
			// `null` instead of `[]`), matching the "kind exists but has zero
			// versions" case the service already returns successfully.
			utils.WriteSuccessResponse(w, http.StatusOK, []models.AgentKindVersionResponse{})
			return
		}
		log.Error("Failed to list agent kind versions", "error", err)
		handleCommonErrors(w, err, "Failed to list agent kind versions")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, result)
}

func (c *agentKindController) GetVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)
	kindName := r.PathValue(utils.PathParamKindName)
	versionTag := r.PathValue(utils.PathParamVersionTag)

	result, err := c.kindService.GetVersion(ctx, ouID, kindName, versionTag)
	if err != nil {
		log.Error("Failed to get agent kind version", "error", err)
		handleCommonErrors(w, err, "Failed to get agent kind version")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, result)
}

func (c *agentKindController) DeleteVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)
	kindName := r.PathValue(utils.PathParamKindName)
	versionTag := r.PathValue(utils.PathParamVersionTag)

	if err := c.kindService.DeleteVersion(ctx, ouID, kindName, versionTag); err != nil {
		log.Error("Failed to delete agent kind version", "error", err)
		handleCommonErrors(w, err, "Failed to delete agent kind version")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *agentKindController) ListKindAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.GetLogger(ctx)
	ouID := middleware.OUIDFromRequest(r)
	kindName := r.PathValue(utils.PathParamKindName)

	result, err := c.kindService.ListKindAgents(ctx, ouID, kindName)
	if err != nil {
		log.Error("Failed to list agents for kind", "error", err)
		handleCommonErrors(w, err, "Failed to list agents for kind")
		return
	}
	utils.WriteSuccessResponse(w, http.StatusOK, result)
}
