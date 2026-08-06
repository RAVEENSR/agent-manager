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

package repositories

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wso2/agent-manager/agent-manager-service/models"
)

// OrgSchedulerCredentialRepository defines the interface for per-org scheduler credential data access
//
//go:generate moq -rm -fmt goimports -skip-ensure -pkg repomocks -out repomocks/org_scheduler_credential_repository_mock.go . OrgSchedulerCredentialRepository:OrgSchedulerCredentialRepositoryMock
type OrgSchedulerCredentialRepository interface {
	GetByOrgName(ouID string) (*models.OrgSchedulerCredential, error)
	Upsert(cred *models.OrgSchedulerCredential) error
}

type orgSchedulerCredentialRepo struct {
	db *gorm.DB
}

// NewOrgSchedulerCredentialRepo creates a new OrgSchedulerCredentialRepository
func NewOrgSchedulerCredentialRepo(db *gorm.DB) OrgSchedulerCredentialRepository {
	return &orgSchedulerCredentialRepo{db: db}
}

// GetByOrgName returns the scheduler credentials for the given org.
// Returns (nil, gorm.ErrRecordNotFound) if no record exists; returns (nil, err) on other DB errors.
func (r *orgSchedulerCredentialRepo) GetByOrgName(ouID string) (*models.OrgSchedulerCredential, error) {
	var cred models.OrgSchedulerCredential
	result := r.db.Where("ou_id = ?", ouID).First(&cred)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, result.Error
	}
	return &cred, nil
}

// Upsert atomically creates or updates scheduler credentials for an org.
func (r *orgSchedulerCredentialRepo) Upsert(cred *models.OrgSchedulerCredential) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "ou_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"org_uuid", "client_id", "secret_kv_path", "secret_key", "client_secret_encrypted", "updated_at"}),
	}).Create(cred).Error
}
