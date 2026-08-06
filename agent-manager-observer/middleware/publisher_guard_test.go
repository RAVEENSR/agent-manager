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

package middleware

import (
	"net/http"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// signToken creates a throwaway HS256-signed token carrying the given
// audience claim. ParseUnverifiedClaims only re-parses tokens without
// verifying the signature, so an arbitrary signing key is fine here.
func signToken(t *testing.T, aud string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"aud": aud})
	signed, err := token.SignedString([]byte("k"))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signed
}

func passThroughHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestParseUnverifiedClaims(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		wantAud    string // empty means nil claims expected
	}{
		{name: "publisher audience", authHeader: "Bearer " + signToken(t, "amp-publisher-acme"), wantAud: "amp-publisher-acme"},
		{name: "normal audience", authHeader: "Bearer " + signToken(t, "localhost"), wantAud: "localhost"},
		{name: "empty header", authHeader: ""},
		{name: "non-bearer scheme", authHeader: "Basic dXNlcjpwYXNz"},
		{name: "garbled token", authHeader: "Bearer not-a-valid-jwt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := ParseUnverifiedClaims(tc.authHeader)
			if tc.wantAud == "" {
				if claims != nil {
					t.Errorf("ParseUnverifiedClaims(%q) = %+v, want nil", tc.authHeader, claims)
				}
				return
			}
			if claims == nil {
				t.Fatalf("ParseUnverifiedClaims(%q) = nil, want audience %q", tc.authHeader, tc.wantAud)
			}
			if len(claims.Audience) != 1 || claims.Audience[0] != tc.wantAud {
				t.Errorf("ParseUnverifiedClaims(%q) audience = %v, want [%s]", tc.authHeader, claims.Audience, tc.wantAud)
			}
		})
	}
}
