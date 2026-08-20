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

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2/agent-manager/agent-manager-service/config"
	"github.com/wso2/agent-manager/agent-manager-service/services"
)

// stubThunderAskEnvironmentService implements services.EnvironmentService by
// embedding the interface (every unimplemented method panics if called) and
// overriding only ThunderHandleRegistered, the one method this route uses.
type stubThunderAskEnvironmentService struct {
	services.EnvironmentService
	registered    bool
	registeredErr error
	calledWith    *string
}

func (s *stubThunderAskEnvironmentService) ThunderHandleRegistered(_ context.Context, handle string) (bool, error) {
	s.calledWith = &handle
	return s.registered, s.registeredErr
}

func setupThunderAskMux(t *testing.T, svc services.EnvironmentService) *http.ServeMux {
	t.Helper()
	cfg := config.GetConfig()
	orig := cfg.ThunderHostBaseDomain
	cfg.ThunderHostBaseDomain = "amp.example.com"
	t.Cleanup(func() { cfg.ThunderHostBaseDomain = orig })

	// A fresh, generous limiter per test — the package-level one is shared
	// production state and would otherwise accumulate hits across every test
	// in this file, making pass/fail depend on run order and timing.
	origLimiter := thunderAskRateLimit
	thunderAskRateLimit = newTokenBucketLimiter(1000, 1000)
	t.Cleanup(func() { thunderAskRateLimit = origLimiter })

	mux := http.NewServeMux()
	registerThunderAskRoute(mux, svc)
	return mux
}

func askThunderRoute(mux *http.ServeMux, domain string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/internal/thunder-ask?domain="+domain, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestThunderAskRoute_RegisteredHandleAllowed(t *testing.T) {
	svc := &stubThunderAskEnvironmentService{registered: true}
	mux := setupThunderAskMux(t, svc)

	rec := askThunderRoute(mux, "abcd1234.amp.example.com")

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for a registered handle, got %d", rec.Code)
	}
	if svc.calledWith == nil || *svc.calledWith != "abcd1234" {
		t.Errorf("expected the bare label to be checked, got %v", svc.calledWith)
	}
}

func TestThunderAskRoute_UnregisteredHandleForbidden(t *testing.T) {
	svc := &stubThunderAskEnvironmentService{registered: false}
	mux := setupThunderAskMux(t, svc)

	rec := askThunderRoute(mux, "never-claimed.amp.example.com")

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for an unregistered handle, got %d", rec.Code)
	}
}

func TestThunderAskRoute_FailsClosedOnServiceError(t *testing.T) {
	svc := &stubThunderAskEnvironmentService{registeredErr: errors.New("db down")}
	mux := setupThunderAskMux(t, svc)

	rec := askThunderRoute(mux, "abcd1234.amp.example.com")

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when the registry can't be read, got %d", rec.Code)
	}
}

func TestThunderAskRoute_NonThunderHostAlwaysAllowed(t *testing.T) {
	// The gateway and deployed-agent wildcards are matched and allowed in
	// Caddy itself before ever reaching here (see caddyfile()) — this route
	// must never touch the handle registry for hostnames outside the
	// env-Thunder base domain.
	svc := &stubThunderAskEnvironmentService{}
	mux := setupThunderAskMux(t, svc)

	rec := askThunderRoute(mux, "some-org.gateway.example.com")

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for a non-Thunder host, got %d", rec.Code)
	}
	if svc.calledWith != nil {
		t.Errorf("expected the handle registry not to be consulted, got %v", svc.calledWith)
	}
}

func TestThunderAskRoute_EmptyLabelForbidden(t *testing.T) {
	svc := &stubThunderAskEnvironmentService{}
	mux := setupThunderAskMux(t, svc)

	rec := askThunderRoute(mux, ".amp.example.com")

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for an empty label, got %d", rec.Code)
	}
	if svc.calledWith != nil {
		t.Errorf("expected the handle registry not to be consulted, got %v", svc.calledWith)
	}
}

func TestThunderAskRoute_DottedLabelForbiddenWithoutConsultingRegistry(t *testing.T) {
	// Every registered handle is a single DNS label (see validateThunderHandle) —
	// a dotted label is never valid, no matter what the registry says, so it
	// must be rejected before ever calling ThunderHandleRegistered.
	svc := &stubThunderAskEnvironmentService{registered: true}
	mux := setupThunderAskMux(t, svc)

	rec := askThunderRoute(mux, "myorg-myenv.thunder.amp.example.com")

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for a dotted label, got %d", rec.Code)
	}
	if svc.calledWith != nil {
		t.Errorf("expected the handle registry not to be consulted for a dotted label, got %v", svc.calledWith)
	}
}

func TestThunderAskRoute_RateLimited(t *testing.T) {
	svc := &stubThunderAskEnvironmentService{registered: true}
	mux := http.NewServeMux()
	origLimiter := thunderAskRateLimit
	thunderAskRateLimit = newTokenBucketLimiter(1, 1)
	t.Cleanup(func() { thunderAskRateLimit = origLimiter })
	cfg := config.GetConfig()
	origDomain := cfg.ThunderHostBaseDomain
	cfg.ThunderHostBaseDomain = "amp.example.com"
	t.Cleanup(func() { cfg.ThunderHostBaseDomain = origDomain })
	registerThunderAskRoute(mux, svc)

	first := askThunderRoute(mux, "abcd1234.amp.example.com")
	second := askThunderRoute(mux, "abcd1234.amp.example.com")

	if first.Code != http.StatusOK {
		t.Errorf("expected the first request within burst to succeed, got %d", first.Code)
	}
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("expected the request past burst to be rate-limited, got %d", second.Code)
	}
}

func askThunderRouteWithSecret(mux *http.ServeMux, domain, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/internal/thunder-ask?domain="+domain, nil)
	if secret != "" {
		req.Header.Set(thunderAskSecretHeader, secret)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestThunderAskRoute_TrustedSecretHasItsOwnBudget locks in the actual fix for
// the shared-limiter problem: a public caller exhausting thunderAskRateLimit
// must never be able to deny a legitimate Caddy request presenting
// ThunderAskSecret, because that request draws from a separate budget.
func TestThunderAskRoute_TrustedSecretHasItsOwnBudget(t *testing.T) {
	svc := &stubThunderAskEnvironmentService{registered: true}
	mux := http.NewServeMux()

	origPublic, origTrusted := thunderAskRateLimit, thunderAskTrustedRateLimit
	thunderAskRateLimit = newTokenBucketLimiter(0, 1) // exactly one public request ever
	thunderAskTrustedRateLimit = newTokenBucketLimiter(1000, 1000)
	t.Cleanup(func() { thunderAskRateLimit, thunderAskTrustedRateLimit = origPublic, origTrusted })

	cfg := config.GetConfig()
	origDomain, origSecret := cfg.ThunderHostBaseDomain, cfg.ThunderAskSecret
	cfg.ThunderHostBaseDomain = "amp.example.com"
	cfg.ThunderAskSecret = "s3cr3t"
	t.Cleanup(func() { cfg.ThunderHostBaseDomain, cfg.ThunderAskSecret = origDomain, origSecret })

	registerThunderAskRoute(mux, svc)

	// Exhaust the public budget entirely.
	exhaust := askThunderRoute(mux, "abcd1234.amp.example.com")
	if exhaust.Code != http.StatusOK {
		t.Fatalf("expected the exhausting public request to succeed, got %d", exhaust.Code)
	}
	blocked := askThunderRoute(mux, "abcd1234.amp.example.com")
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the public budget to now be exhausted, got %d", blocked.Code)
	}

	// A request presenting the correct secret must still succeed.
	trusted := askThunderRouteWithSecret(mux, "abcd1234.amp.example.com", "s3cr3t")
	if trusted.Code != http.StatusOK {
		t.Errorf("expected a request with a valid ThunderAskSecret to draw from its own budget, got %d", trusted.Code)
	}
}

func TestThunderAskRoute_WrongSecretStaysOnPublicBudget(t *testing.T) {
	svc := &stubThunderAskEnvironmentService{registered: true}
	mux := http.NewServeMux()

	origPublic, origTrusted := thunderAskRateLimit, thunderAskTrustedRateLimit
	thunderAskRateLimit = newTokenBucketLimiter(0, 1)
	thunderAskTrustedRateLimit = newTokenBucketLimiter(1000, 1000)
	t.Cleanup(func() { thunderAskRateLimit, thunderAskTrustedRateLimit = origPublic, origTrusted })

	cfg := config.GetConfig()
	origDomain, origSecret := cfg.ThunderHostBaseDomain, cfg.ThunderAskSecret
	cfg.ThunderHostBaseDomain = "amp.example.com"
	cfg.ThunderAskSecret = "s3cr3t"
	t.Cleanup(func() { cfg.ThunderHostBaseDomain, cfg.ThunderAskSecret = origDomain, origSecret })

	registerThunderAskRoute(mux, svc)

	askThunderRoute(mux, "abcd1234.amp.example.com") // exhausts the public budget

	wrongSecret := askThunderRouteWithSecret(mux, "abcd1234.amp.example.com", "not-the-secret")
	if wrongSecret.Code != http.StatusTooManyRequests {
		t.Errorf("expected an invalid secret to stay on the exhausted public budget, got %d", wrongSecret.Code)
	}
}

// TestThunderAskRoute_UnconfiguredSecretPreservesExistingBehavior guards the
// upgrade path: a deployment that hasn't set ThunderAskSecret (the zero value)
// must keep exactly today's single-limiter behavior, even if a caller sends
// the header — there's nothing configured to compare it against.
func TestThunderAskRoute_UnconfiguredSecretPreservesExistingBehavior(t *testing.T) {
	svc := &stubThunderAskEnvironmentService{registered: true}
	mux := setupThunderAskMux(t, svc)

	rec := askThunderRouteWithSecret(mux, "abcd1234.amp.example.com", "anything")

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for a registered handle regardless of the header, got %d", rec.Code)
	}
}
