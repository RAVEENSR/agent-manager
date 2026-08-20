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
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wso2/agent-manager/agent-manager-service/config"
	"github.com/wso2/agent-manager/agent-manager-service/middleware/logger"
	"github.com/wso2/agent-manager/agent-manager-service/services"
)

// thunderAskSecretHeader is the header Caddy's ask reverse_proxy attaches
// (see caddyfile() in deployments/vm/lib-vm.sh) carrying ThunderAskSecret.
const thunderAskSecretHeader = "X-Thunder-Ask-Secret"

// tokenBucketLimiter is a minimal stdlib-only token bucket — deliberately
// hand-rolled rather than pulling in an external rate-limiting package for
// this one narrow use.
type tokenBucketLimiter struct {
	mu         sync.Mutex
	tokens     float64
	max        float64
	refillRate float64 // tokens per second
	last       time.Time
}

func newTokenBucketLimiter(refillPerSecond, burst float64) *tokenBucketLimiter {
	return &tokenBucketLimiter{tokens: burst, max: burst, refillRate: refillPerSecond, last: time.Now()}
}

func (l *tokenBucketLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens = min(l.max, l.tokens+elapsed*l.refillRate)
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

// thunderAskRateLimit bounds how fast this reachable-from-the-public-internet
// endpoint (routed through the api host's own catch-all HTTPRoute so Caddy can
// reach it — see caddyfile()) can be used to enumerate registered handles.
// Applies to every caller that does NOT present a valid ThunderAskSecret —
// which, when the secret isn't configured at all, means every caller,
// preserving today's behavior exactly for a deployment that hasn't set one.
var thunderAskRateLimit = newTokenBucketLimiter(5, 10)

// thunderAskTrustedRateLimit is the separate, much more generous budget for
// calls presenting a valid ThunderAskSecret (i.e., genuinely from Caddy — see
// thunderAskSecretHeader). Sharing ONE limiter between Caddy and the public
// internet let a burst of public requests exhaust the budget and 429 the next
// legitimate Caddy request, denying TLS issuance for a brand new environment.
// Still bounded (not skipped outright) as a guard against Caddy itself
// retry-storming, but high enough to never matter for real usage — new-cert
// issuance and renewals are rare events.
var thunderAskTrustedRateLimit = newTokenBucketLimiter(50, 100)

// registerThunderAskRoute registers the endpoint Caddy's on-demand TLS "ask"
// mechanism calls before issuing a certificate for any hostname under the
// env-Thunder wildcard. A valid handle is always a single DNS label (see
// validateThunderHandle) — a dotted label is rejected outright, without ever
// consulting the registry. caddyfile() (deployments/vm/lib-vm.sh) matches the
// per-env gateway and deployed-agent wildcards BEFORE ever proxying here, so
// every request this handler actually sees is already known to be
// env-Thunder-shaped.
//
// The route itself stays open to the public internet (Caddy's ask client
// can't authenticate any other way, and this endpoint is reachable through
// the api host's own catch-all HTTPRoute) — ThunderAskSecret only shields
// Caddy's OWN rate-limit budget from that public reachability, it does not
// gate the answer itself. The question answered here ("is this label a
// registered env-Thunder handle?") leaks nothing beyond what's already
// inferable by directly dialing the hostname and observing whether it
// resolves.
func registerThunderAskRoute(mux *http.ServeMux, environmentService services.EnvironmentService) {
	mux.HandleFunc("GET /internal/thunder-ask", func(w http.ResponseWriter, r *http.Request) {
		limiter := thunderAskRateLimit
		if secret := config.GetConfig().ThunderAskSecret; secret != "" &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get(thunderAskSecretHeader)), []byte(secret)) == 1 {
			limiter = thunderAskTrustedRateLimit
		}
		if !limiter.Allow() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}

		domain := r.URL.Query().Get("domain")
		label, isEnvThunderHost := strings.CutSuffix(domain, "."+config.GetConfig().ThunderHostBaseDomain)
		if !isEnvThunderHost {
			w.WriteHeader(http.StatusOK)
			return
		}
		if label == "" || strings.Contains(label, ".") {
			// The bare base domain is never a valid handle, and neither is a
			// dotted label — every registered handle is a single DNS label
			// (see validateThunderHandle).
			w.WriteHeader(http.StatusForbidden)
			return
		}

		registered, err := environmentService.ThunderHandleRegistered(r.Context(), label)
		if err != nil {
			// Fail closed: an unreadable registry must never be treated as
			// authorization to issue the certificate.
			logger.GetLogger(r.Context()).Error("thunder-ask: failed to check handle registration", "handle", label, "error", err)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !registered {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
