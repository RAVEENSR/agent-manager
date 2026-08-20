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
	"slices"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Restore ingress coverage that migration 037 removed.
//
// 037's ai -> egress arm assumed 'ai' was only ever a second gateway installed beside
// a 'regular' one, which holds for the OSS paths but not for provisioners that register
// an environment's ONLY gateway as 'ai'. Those environments came out of 037 with no
// ingress-capable gateway, and resolveEnvGateways now filters on IngressGatewayRoles, so
// they can no longer be issued an inbound agent API key at all.
//
// Promote one egress gateway to 'both' in each environment that has none — which is what
// those environments effectively were before 037, whose resolver ignored the type column
// and keyed every gateway mapped to the environment. 'both' rather than 'ingress' so the
// gateway keeps carrying its existing LLM/MCP traffic. Environments that already hold an
// 'ingress' or 'both' gateway are untouched, leaving the regular+ai pair and split
// topology alone.

var migration041 = migration{
	ID: 41,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := PromoteSoleEgressGateways(tx); err != nil {
				return err
			}
			return AssertIngressCoveragePerEnvironment(tx)
		})
	},
}

// promotionCandidate is an egress gateway eligible for promotion, with the environments
// it would cover as a comma-separated list.
type promotionCandidate struct {
	UUID uuid.UUID `gorm:"column:uuid"`
	Envs string    `gorm:"column:envs"`
}

// PromoteSoleEgressGateways promotes egress gateways to 'both' so that every environment
// currently without an ingress-capable gateway gets one.
//
// A gateway's role applies in every environment it is mapped to at once, so a candidate
// must map only to uncovered environments — otherwise promoting it would breach the
// one-ingress cap in an environment that already has coverage. Among those candidates,
// the ones covering the most uncovered environments are taken first, so a gateway shared
// by several uncovered environments covers them all in one promotion rather than being
// passed over in favour of a single-environment gateway. created_at then uuid break ties.
// Two promoted gateways may not share an environment, since that environment would then
// hold two ingress-capable gateways.
//
// The greedy pick is not proven minimal for arbitrarily overlapping mappings; anything it
// leaves uncovered is reported by AssertIngressCoveragePerEnvironment rather than passing
// silently.
//
// deleted_at IS NULL because raw SQL bypasses GORM's soft-delete scope. is_active is
// deliberately not filtered — it tracks WebSocket liveness and flaps, so a gateway that
// happens to be disconnected during the upgrade must still be promoted.
func PromoteSoleEgressGateways(tx *gorm.DB) error {
	const q = `
		WITH live AS (
			SELECT g.uuid,
			       g.created_at,
			       g.gateway_functionality_type AS role,
			       m.environment_uuid
			FROM gateways g
			JOIN gateway_environment_mappings m ON m.gateway_uuid = g.uuid
			WHERE g.deleted_at IS NULL
		),
		uncovered AS (
			SELECT DISTINCT l.environment_uuid
			FROM live l
			WHERE NOT EXISTS (
				SELECT 1 FROM live l2
				WHERE l2.environment_uuid = l.environment_uuid
				  AND l2.role IN ('ingress', 'both')
			)
		)
		SELECT l.uuid,
		       string_agg(l.environment_uuid::text, ',' ORDER BY l.environment_uuid) AS envs
		FROM live l
		LEFT JOIN uncovered u ON u.environment_uuid = l.environment_uuid
		WHERE l.role = 'egress'
		GROUP BY l.uuid, l.created_at
		HAVING bool_and(u.environment_uuid IS NOT NULL)
		ORDER BY count(*) DESC, l.created_at, l.uuid;`

	var candidates []promotionCandidate
	if err := tx.Raw(q).Scan(&candidates).Error; err != nil {
		return fmt.Errorf("migration 041: collecting promotion candidates failed: %w", err)
	}

	claimed := make(map[string]bool)
	var promote []uuid.UUID
	for _, c := range candidates {
		envs := strings.Split(c.Envs, ",")
		if slices.ContainsFunc(envs, func(env string) bool { return claimed[env] }) {
			continue
		}
		for _, env := range envs {
			claimed[env] = true
		}
		promote = append(promote, c.UUID)
	}
	if len(promote) == 0 {
		return nil
	}

	if err := tx.Exec(
		`UPDATE gateways SET gateway_functionality_type = 'both', updated_at = now()
		 WHERE uuid IN ?`, promote,
	).Error; err != nil {
		return fmt.Errorf("migration 041: promoting egress gateways failed: %w", err)
	}
	return nil
}

// uncoveredEnvRow is one environment left without an ingress-capable gateway.
type uncoveredEnvRow struct {
	EnvironmentUUID string `gorm:"column:environment_uuid"`
	Gateways        string `gorm:"column:gateways"`
}

// AssertIngressCoveragePerEnvironment aborts the migration when an environment with
// gateways mapped to it holds none that can serve ingress. It is the lower bound to
// 037's AssertSingleIngressPerEnvironment upper bound, and the check whose absence let
// 037 leave environments silently unable to issue an inbound API key.
//
// What reaches here is a gateway shared across environments that lost the pick in one of
// them. The role is immutable once registered, so an operator has to resolve that in SQL
// either way; failing the upgrade surfaces it while it is still cheap. Environments with
// no gateways at all are not a regression and are not reported.
func AssertIngressCoveragePerEnvironment(tx *gorm.DB) error {
	const q = `
		SELECT m.environment_uuid,
		       string_agg(g.name || ' (' || g.gateway_functionality_type || ')', ', '
		                  ORDER BY g.name) AS gateways
		FROM gateway_environment_mappings m
		JOIN gateways g ON g.uuid = m.gateway_uuid
		WHERE g.deleted_at IS NULL
		  AND NOT EXISTS (
			SELECT 1
			FROM gateway_environment_mappings m2
			JOIN gateways g2 ON g2.uuid = m2.gateway_uuid
			WHERE m2.environment_uuid = m.environment_uuid
			  AND g2.deleted_at IS NULL
			  AND g2.gateway_functionality_type IN ('ingress', 'both')
		  )
		GROUP BY m.environment_uuid
		ORDER BY m.environment_uuid;`

	var rows []uncoveredEnvRow
	if err := tx.Raw(q).Scan(&rows).Error; err != nil {
		return fmt.Errorf("migration 041: ingress coverage check failed: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("migration 041 aborted: the environments below hold gateways but none that can " +
		"serve ingress ('ingress' or 'both'), so no agent in them can be issued an inbound API " +
		"key. The automatic promotion skipped these because the candidate gateway also serves an " +
		"environment that already has an ingress-capable gateway, and the role applies to the " +
		"gateway in every environment at once.\n\n" +
		"Promote one gateway per environment below, or register a new ingress gateway there. " +
		"This transaction has rolled back, so nothing has changed yet.\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "\n  environment %s: %s\n", r.EnvironmentUUID, r.Gateways)
		fmt.Fprintf(&b, "      UPDATE gateways SET gateway_functionality_type = 'both' WHERE uuid = '<pick one>';\n")
	}
	return errors.New(b.String())
}
