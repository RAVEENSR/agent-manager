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

package models

import (
	"time"

	"github.com/google/uuid"
)

// EnvThunderURL is an unguessable handle (user-chosen or server-generated)
// that forms an environment's externally-reachable env-Thunder hostname
// ("<handle>.<baseDomain>"), keyed by (OUID, EnvName). ThunderHandle is
// additionally globally unique across all orgs/envs — see migration042's doc
// comment for why.
type EnvThunderURL struct {
	ID            uuid.UUID `gorm:"column:id;primaryKey;type:uuid;default:gen_random_uuid()"`
	OUID          string    `gorm:"column:ou_id;not null;uniqueIndex:uq_env_thunder_urls_ou_env"`
	EnvName       string    `gorm:"column:env_name;not null;uniqueIndex:uq_env_thunder_urls_ou_env"`
	ThunderHandle string    `gorm:"column:thunder_handle;not null;uniqueIndex:uq_env_thunder_urls_handle"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;default:NOW()"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null;default:NOW()"`
}

// TableName returns the table name for the EnvThunderURL model.
func (EnvThunderURL) TableName() string { return "env_thunder_urls" }
