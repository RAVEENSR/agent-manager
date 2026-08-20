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

package dbmigrations

import (
	"gorm.io/gorm"
)

// env_thunder_urls stores an unguessable handle (user-chosen or server-generated)
// that forms an environment's externally-reachable env-Thunder hostname
// (issuer/token/JWKS URLs). Without this, every env-Thunder URL would be 100%
// derivable from (org, env) alone, letting an external attacker construct a
// live instance's endpoints without legitimate access.
//
// Keyed by (ou_id, env_name), not org_name — same multi-tenant-safe rationale
// as env_thunder_system_clients (see migration036's doc comment).
//
// Two unique constraints:
//   - uq_env_thunder_urls_ou_env: one handle per environment. Once a row exists
//     for an environment, it's insert-only from then on — a second insert for
//     the same (ou_id, env_name) is rejected rather than overwriting the row,
//     since Thunder's issuer is immutable once minted (see
//     repositories.EnvThunderURLRepository.Insert).
//   - uq_env_thunder_urls_handle: the handle is globally unique across ALL
//     orgs/envs, because every env-Thunder instance's HTTPRoute lives in the
//     same shared namespace and attaches to the same shared Gateway — hostname
//     routing is cluster-wide, not per-org, so two environments must never be
//     able to claim the same external hostname.
var migration042 = migration{
	ID: 42,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			createTable := `
			CREATE TABLE IF NOT EXISTS env_thunder_urls (
				id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				ou_id          VARCHAR(255) NOT NULL,
				env_name       VARCHAR(255) NOT NULL,
				thunder_handle VARCHAR(80) NOT NULL,
				created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

				CONSTRAINT uq_env_thunder_urls_ou_env UNIQUE (ou_id, env_name),
				CONSTRAINT uq_env_thunder_urls_handle UNIQUE (thunder_handle)
			)`

			return runSQL(tx, createTable)
		})
	},
}
