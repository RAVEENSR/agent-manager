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

// org_scheduler_credentials holds the scheduler-only OAuth2 credential, split out from org_publisher_credentials.
var migration039 = migration{
	ID: 39,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			createTable := `
			CREATE TABLE IF NOT EXISTS org_scheduler_credentials (
				id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				ou_id                   VARCHAR(255) NOT NULL,
				org_uuid                VARCHAR(255) NOT NULL DEFAULT '',
				client_id               VARCHAR(255) NOT NULL,
				secret_kv_path          VARCHAR(255) NOT NULL,
				secret_key              VARCHAR(255) NOT NULL DEFAULT 'client-secret',
				client_secret_encrypted BYTEA NOT NULL,
				created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

				CONSTRAINT uq_org_scheduler_creds_ou_id UNIQUE (ou_id)
			)`
			return runSQL(tx, createTable)
		})
	},
}
