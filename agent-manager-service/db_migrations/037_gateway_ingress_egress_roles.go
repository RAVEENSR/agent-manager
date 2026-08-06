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
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Replace the regular/ai gateway vocabulary with ingress/egress/both.
//
// regular -> both and ai -> egress, because every gateway provisioned to date is
// "regular" and back-filling it to "both" is what makes deleting the
// fallback-to-any-active-gateway in the resolvers safe. Mapping regular to "ingress"
// would leave those environments with zero egress-capable gateways and fail every
// LLM/MCP deploy.
//
// The DEFAULT is dropped rather than retargeted: the cap is enforced only in
// application code, so any ingress-capable default would let a raw INSERT, a future
// migration or a seeder silently mint a gateway that bypasses it. With no default such
// an insert fails cleanly on NOT NULL instead.
var migration037 = migration{
	ID: 37,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := runSQL(
				tx,
				`ALTER TABLE gateways DROP CONSTRAINT IF EXISTS chk_gateway_functionality_type;`,
				`UPDATE gateways SET gateway_functionality_type = 'both'
				   WHERE gateway_functionality_type = 'regular';`,
				`UPDATE gateways SET gateway_functionality_type = 'egress'
				   WHERE gateway_functionality_type = 'ai';`,
			); err != nil {
				return err
			}

			// Must run after the back-fill so it observes 'both'.
			if err := AssertSingleIngressPerEnvironment(tx); err != nil {
				return err
			}

			return runSQL(
				tx,
				`ALTER TABLE gateways ALTER COLUMN gateway_functionality_type DROP DEFAULT;`,
				`ALTER TABLE gateways ADD CONSTRAINT chk_gateway_functionality_type
				   CHECK (gateway_functionality_type IN ('ingress', 'egress', 'both'));`,
				`COMMENT ON COLUMN gateways.gateway_functionality_type IS
				   'Placement role: ingress (inbound agent traffic, at most one per environment), egress (outbound LLM/MCP traffic, uncapped), or both';`,
			)
		})
	},
}

// ingressConflictRow is one offending (environment, gateway) pair from the cap guard.
type ingressConflictRow struct {
	EnvironmentUUID string `gorm:"column:environment_uuid"`
	GatewayUUID     string `gorm:"column:gateway_uuid"`
	GatewayName     string `gorm:"column:name"`
}

// AssertSingleIngressPerEnvironment aborts the migration when any environment holds two
// or more ingress-capable gateways, since the new model caps ingress at one and the role
// is immutable afterwards — SQL is the operator's only recourse, permanently.
//
// Counts ingress-capable only: egress is uncapped, which is what lets the regular+ai pair
// produced by `make setup-ai-gateway` back-fill to both+egress and upgrade cleanly.
//
// deleted_at IS NULL is required because this is raw SQL and therefore bypasses GORM's
// soft-delete scope; legacy soft-deleted gateway rows (see migration 009) would otherwise
// abort upgrades for nothing. is_active is deliberately NOT filtered — it is WebSocket
// liveness and flaps.
func AssertSingleIngressPerEnvironment(tx *gorm.DB) error {
	const q = `
		SELECT m.environment_uuid, g.uuid AS gateway_uuid, g.name
		FROM gateway_environment_mappings m
		JOIN gateways g ON g.uuid = m.gateway_uuid
		WHERE g.deleted_at IS NULL
		  AND g.gateway_functionality_type IN ('ingress', 'both')
		  AND m.environment_uuid IN (
			SELECT m2.environment_uuid
			FROM gateway_environment_mappings m2
			JOIN gateways g2 ON g2.uuid = m2.gateway_uuid
			WHERE g2.deleted_at IS NULL
			  AND g2.gateway_functionality_type IN ('ingress', 'both')
			GROUP BY m2.environment_uuid
			HAVING count(*) > 1
		  )
		ORDER BY m.environment_uuid, g.name;`

	var rows []ingressConflictRow
	if err := tx.Raw(q).Scan(&rows).Error; err != nil {
		return fmt.Errorf("migration 037: ingress cap pre-check failed: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("migration 037 aborted: an environment may hold at most one ingress-capable " +
		"gateway (role 'ingress' or 'both'), and the role is immutable once registered, so no API " +
		"can correct this before or after the upgrade.\n\n" +
		"For each environment below, keep ONE gateway as ingress and demote the rest. This " +
		"transaction has rolled back, so the pre-migration CHECK is live again and 'ai' is the " +
		"only writable value that back-fills to 'egress' on the next run. Then re-run the " +
		"migration. Deleting the surplus gateway also works.\n")
	currentEnv := ""
	for _, r := range rows {
		if r.EnvironmentUUID != currentEnv {
			currentEnv = r.EnvironmentUUID
			fmt.Fprintf(&b, "\n  environment %s:\n", currentEnv)
		}
		fmt.Fprintf(&b, "    gateway %q (%s)\n", r.GatewayName, r.GatewayUUID)
		fmt.Fprintf(&b, "      UPDATE gateways SET gateway_functionality_type = 'ai' WHERE uuid = '%s';\n",
			r.GatewayUUID)
	}
	return errors.New(b.String())
}
