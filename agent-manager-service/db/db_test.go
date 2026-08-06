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

package db

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/wso2/agent-manager/agent-manager-service/config"
)

func basePostgresConfig() config.POSTGRESQL {
	return config.POSTGRESQL{
		Host:     "db.example.com",
		Port:     5432,
		User:     "agentmanager",
		Password: "s3cr#t/pass",
		DBName:   "agentmanager",
	}
}

func TestMakeConnString(t *testing.T) {
	tests := []struct {
		name        string
		sslMode     string
		sslRootCert string
		want        string
	}{
		{
			name: "no TLS settings keeps the pre-existing DSN with no query string",
			want: "postgres://agentmanager:s3cr%23t%2Fpass@db.example.com:5432/agentmanager",
		},
		{
			name:    "sslmode is appended when set",
			sslMode: "require",
			want:    "postgres://agentmanager:s3cr%23t%2Fpass@db.example.com:5432/agentmanager?sslmode=require",
		},
		{
			name:        "sslrootcert is appended alongside the mode",
			sslMode:     "verify-full",
			sslRootCert: "/etc/amp/db-ca/ca.crt",
			want:        "postgres://agentmanager:s3cr%23t%2Fpass@db.example.com:5432/agentmanager?sslmode=verify-full&sslrootcert=%2Fetc%2Famp%2Fdb-ca%2Fca.crt",
		},
		{
			name:        "sslrootcert alone is appended",
			sslRootCert: "system",
			want:        "postgres://agentmanager:s3cr%23t%2Fpass@db.example.com:5432/agentmanager?sslrootcert=system",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := basePostgresConfig()
			cfg.SSLMode = tc.sslMode
			cfg.SSLRootCert = tc.sslRootCert

			if got := makeConnString(cfg); got != tc.want {
				t.Errorf("makeConnString() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMakeConnStringIsParsedByDriver guards the contract that matters in
// production: whatever makeConnString emits must survive the pgx parser with the
// requested TLS mode intact. It also pins the fallback semantics that make the
// empty default safe — "prefer" keeps a plaintext fallback, "require" does not.
func TestMakeConnStringIsParsedByDriver(t *testing.T) {
	tests := []struct {
		name          string
		sslMode       string
		wantTLS       bool
		wantFallbacks int
	}{
		{
			name:          "empty mode yields the libpq prefer default with a plaintext fallback",
			sslMode:       "",
			wantTLS:       true,
			wantFallbacks: 1,
		},
		{
			name:          "require yields TLS with no plaintext fallback",
			sslMode:       "require",
			wantTLS:       true,
			wantFallbacks: 0,
		},
		{
			name:          "disable yields no TLS",
			sslMode:       "disable",
			wantTLS:       false,
			wantFallbacks: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// PGSSLMODE in the ambient environment would override the empty case.
			t.Setenv("PGSSLMODE", "")

			cfg := basePostgresConfig()
			cfg.SSLMode = tc.sslMode

			parsed, err := pgx.ParseConfig(makeConnString(cfg))
			if err != nil {
				t.Fatalf("pgx.ParseConfig() returned an unexpected error: %v", err)
			}
			if gotTLS := parsed.TLSConfig != nil; gotTLS != tc.wantTLS {
				t.Errorf("TLSConfig set = %t, want %t", gotTLS, tc.wantTLS)
			}
			if got := len(parsed.Fallbacks); got != tc.wantFallbacks {
				t.Errorf("len(Fallbacks) = %d, want %d", got, tc.wantFallbacks)
			}
		})
	}
}
