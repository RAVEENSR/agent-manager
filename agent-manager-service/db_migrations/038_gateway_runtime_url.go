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
	"os"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// Anchor the gateway's in-cluster runtime address as data instead of deriving it from
// gateways.name at read time. The chart supplies it at registration from here on; this
// migration back-fills existing rows by replaying the derivation AMS applies today, so
// no row changes behaviour on upgrade.
var migration038 = migration{
	ID: 38,
	Migrate: func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := runSQL(
				tx,
				`ALTER TABLE gateways ADD COLUMN IF NOT EXISTS runtime_url TEXT;`,
				`COMMENT ON COLUMN gateways.runtime_url IS 'In-cluster base URL of the gateway runtime, supplied at registration. NULL means no internal address; consumers fall back to vhost';`,
			); err != nil {
				return err
			}
			return BackfillGatewayRuntimeURL(tx)
		})
	},
}

// Defaults duplicated from config_loader.go rather than imported: that loader is deleted
// in the same change, and a migration must stay a frozen snapshot of past behaviour.
const (
	defaultGatewayRuntimeNamePrefix    = "api-platform-"
	defaultGatewayRuntimeServiceSuffix = "-gw-gateway-gateway-runtime"
	defaultGatewayRuntimePort          = 22893
)

// BackfillGatewayRuntimeURL sets runtime_url to whatever gatewayRuntimeInClusterURL would
// have computed for each row. Rows whose name does not carry the configured prefix derived
// "" before this change and are left NULL, which the consumers treat identically.
//
// Reads GATEWAY_RUNTIME_* from the environment for the legacy/compose case, where those
// keys may still be set. The Helm path has already dropped them, so the hardcoded defaults
// below are what actually applies there.
func BackfillGatewayRuntimeURL(tx *gorm.DB) error {
	prefix := strings.TrimSpace(envOrDefault("GATEWAY_RUNTIME_NAME_PREFIX", defaultGatewayRuntimeNamePrefix))
	suffix := strings.TrimSpace(envOrDefault("GATEWAY_RUNTIME_SERVICE_SUFFIX", defaultGatewayRuntimeServiceSuffix))
	port := defaultGatewayRuntimePort
	if raw := strings.TrimSpace(os.Getenv("GATEWAY_RUNTIME_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 65535 {
			port = parsed
		}
	}

	// An empty prefix made TrimPrefix a no-op, so the derivation returned "" for every
	// row. Leaving them all NULL is the faithful replay.
	if prefix == "" {
		return nil
	}

	// strpos(...) = 1 rather than LIKE: the prefix is data and may contain % or _.
	// btrim approximates strings.TrimSpace: it strips ASCII spaces only, not \t\n\v\f\r —
	// unreachable here because names are Kubernetes resource names.
	// length(name) > length(prefix) reproduces the namespace != "" guard.
	// Every parameter is bound as text so the driver never has to infer a type for a
	// value used in string concatenation.
	const q = `
		UPDATE gateways
		SET runtime_url = 'http://' || btrim(name) || ? || '.' ||
		                  substr(btrim(name), length(CAST(? AS text)) + 1) || ':' || ?
		WHERE strpos(btrim(name), ?) = 1
		  AND length(btrim(name)) > length(CAST(? AS text))
		  AND (runtime_url IS NULL OR runtime_url = '');`
	return tx.Exec(q, suffix, prefix, strconv.Itoa(port), prefix, prefix).Error
}

func envOrDefault(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
