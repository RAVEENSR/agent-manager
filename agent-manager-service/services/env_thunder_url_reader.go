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

package services

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/wso2/agent-manager/agent-manager-service/clients/thundersvc"
	"github.com/wso2/agent-manager/agent-manager-service/repositories"
)

// ResolveThunderHandle is the SINGLE place every caller resolves an
// environment's env-Thunder URL handle — EnvironmentService's own
// readThunderHandle/GetThunderURL/SetThunderURL and the resolver's injected
// ReadThunderHandleFunc (via NewEnvThunderURLReader below) all delegate here so
// this logic can never drift apart between call sites.
//
// A missing row means this environment has never been provisioned through
// SetThunderURL — returns ("", nil), never a value computed from (ouID,
// envName). There is no grandfathering: every live env-Thunder instance has a
// row here by construction (SetThunderSystemClientSecret always provisions one
// first), so a missing row means exactly "not provisioned," full stop.
func ResolveThunderHandle(ctx context.Context, urlRepo repositories.EnvThunderURLRepository, ouID, envName string) (string, error) {
	row, err := urlRepo.Get(ctx, ouID, envName)
	if err == nil {
		return row.ThunderHandle, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return "", fmt.Errorf("read env-thunder url handle for %s/%s: %w", ouID, envName, err)
}

// NewEnvThunderURLReader builds the resolver's DB-backed handle reader —
// ResolveThunderHandle widened to thundersvc.ReadThunderHandleFunc's shape.
// Lives in services (not wiring) for the same reason as
// NewEnvThunderSecretReader: app.Run's provisioning factory shares it without a cycle.
func NewEnvThunderURLReader(urlRepo repositories.EnvThunderURLRepository) thundersvc.ReadThunderHandleFunc {
	return func(ctx context.Context, ouID, envName string) (string, error) {
		return ResolveThunderHandle(ctx, urlRepo, ouID, envName)
	}
}
