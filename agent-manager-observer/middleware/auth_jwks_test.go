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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wso2/agent-manager/agent-manager-observer/config"
)

const testKID = "test-key-1"

// newJWKSTestServer generates an RSA key pair, serves its public half as a JWKS
// at the returned URL, and returns the private key for signing test tokens. The
// package-level JWKS cache is reset so tests don't leak keys into one another.
func newJWKSTestServer(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	jwks := JWKS{Keys: []JSONWebKey{{
		Kty: "RSA",
		Kid: testKID,
		N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)

	// Reset the shared cache so a previous test's JWKS can't satisfy this one.
	jwksCacheMutex.Lock()
	jwksCache = nil
	jwksCacheTime = time.Time{}
	jwksCacheMutex.Unlock()

	return key, srv.URL
}

func signRSAToken(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKID
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signed
}

func jwksTestConfig(jwksURL string) config.AuthConfig {
	return config.AuthConfig{
		JWKSUrl:  jwksURL,
		Issuer:   []string{"https://idp.example.com"},
		Audience: []string{"localhost"},
	}
}

// A validly-signed token from a trusted issuer must still be rejected when it
// carries no exp claim, so a mint-once-use-forever token can't slip through.
func TestValidateWithJWKS_RequiresExpiration(t *testing.T) {
	key, jwksURL := newJWKSTestServer(t)
	cfg := jwksTestConfig(jwksURL)

	token := signRSAToken(t, key, jwt.MapClaims{
		"iss": "https://idp.example.com",
		"aud": "localhost",
		// deliberately no "exp"
	})

	if _, err := validateWithJWKS(context.Background(), token, cfg); err == nil {
		t.Fatal("expected token without exp to be rejected, got nil error")
	}
}

func TestValidateWithJWKS_AcceptsValidToken(t *testing.T) {
	key, jwksURL := newJWKSTestServer(t)
	cfg := jwksTestConfig(jwksURL)

	token := signRSAToken(t, key, jwt.MapClaims{
		"iss": "https://idp.example.com",
		"aud": "localhost",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	})

	if _, err := validateWithJWKS(context.Background(), token, cfg); err != nil {
		t.Fatalf("expected valid token to pass, got error: %v", err)
	}
}

func TestValidateWithJWKS_RejectsExpiredToken(t *testing.T) {
	key, jwksURL := newJWKSTestServer(t)
	cfg := jwksTestConfig(jwksURL)

	token := signRSAToken(t, key, jwt.MapClaims{
		"iss": "https://idp.example.com",
		"aud": "localhost",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})

	if _, err := validateWithJWKS(context.Background(), token, cfg); err == nil {
		t.Fatal("expected expired token to be rejected, got nil error")
	}
}
