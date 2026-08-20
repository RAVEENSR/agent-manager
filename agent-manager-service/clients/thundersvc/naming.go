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

package thundersvc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wso2/agent-manager/agent-manager-service/config"
)

const (
	thunderInternalPort = 8090
	maxReleaseNameLen   = 53
	truncatePrefixLen   = 46

	// maxJWKSProbeBodyBytes caps how much of a probe response ThunderProbe reads
	// before validating it as JWKS — a real JWKS document is a few KB at most; this
	// just guards against an unrelated server on a fallback address streaming an
	// unbounded body.
	maxJWKSProbeBodyBytes = 64 * 1024
)

// ThunderReleaseName returns the Helm release name (and namespace) for an env-Thunder instance.
// Mirrors thunder_release_name() in deployments/scripts/thunder-naming.sh — must stay in sync.
// That file is the single source of truth for the bash side of this derivation (sourced by
// every script that provisions, removes, or wires the gateway to env-Thunder); this function
// is the one Go-side copy, since Go can't source bash.
//
// Format: amp-thunder-<org>-<env>, capped at 53 characters.
// If the natural name exceeds 53 chars, it is truncated to 46 characters (trailing "-" stripped)
// and a 6-char hex hash of "org/env" is appended for collision safety.
//
// Deliberately lowercases only — does NOT collapse consecutive hyphens like slugify() does.
// The bash scripts that actually provision Thunder use org/env raw, with no hyphen-collapsing.
// Slugifying here would let this function compute a different address than what was actually
// deployed for any org/env containing "--".
func ThunderReleaseName(org, env string) string {
	org = strings.ToLower(org)
	env = strings.ToLower(env)
	if org == "" || env == "" {
		panic("org and env names must be valid alphanumeric slugs and not empty")
	}
	full := fmt.Sprintf("amp-thunder-%s-%s", org, env)
	if len(full) <= maxReleaseNameLen {
		return strings.TrimSuffix(full, "-")
	}
	hash := thunderSHA6(org + "/" + env)
	prefix := strings.TrimSuffix(full[:truncatePrefixLen], "-")
	return fmt.Sprintf("%s-%s", prefix, hash)
}

// ThunderNamespace returns the Kubernetes namespace for an env-Thunder instance.
// The namespace always mirrors the release name.
func ThunderNamespace(org, env string) string {
	return ThunderReleaseName(org, env)
}

// ThunderInternalURL returns the cluster-internal HTTP URL for an env-Thunder's admin API.
// AMS uses this URL to authenticate and create per-agent OAuth clients.
//
// The Thunder Helm chart creates a K8s Service named "{{ .Release.Name }}-service", so the
// cluster-internal DNS is: http://<release>-service.<namespace>.svc.cluster.local:8090
func ThunderInternalURL(org, env string) string {
	release := ThunderReleaseName(org, env)
	return fmt.Sprintf("http://%s-service.%s.svc.cluster.local:%d", release, release, thunderInternalPort)
}

// ThunderJWKSURL returns the cluster-internal URL for fetching the env-Thunder's JWKS.
// Used by the API gateway's ThunderKeyManager to validate agent tokens.
// INTERNAL ONLY — not reachable outside the cluster; use ThunderExternalJWKSURL for developer-facing output.
func ThunderJWKSURL(org, env string) string {
	return ThunderInternalURL(org, env) + "/oauth2/jwks"
}

// ThunderSystemClientSecretName returns the Kubernetes Secret name that holds the
// system-client credentials for the given env-Thunder instance.
// The secret is created by add-environment-thunder.sh and lives in ThunderNamespace.
func ThunderSystemClientSecretName(org, env string) string {
	return ThunderReleaseName(org, env) + "-system-client"
}

// ThunderTokenURL returns the OAuth2 token endpoint for the env-Thunder instance.
// INTERNAL ONLY — uses cluster-internal K8s DNS (svc.cluster.local:8090); not reachable
// outside the cluster. Use ThunderExternalTokenURL for developer-facing output.
func ThunderTokenURL(org, env string) string {
	return ThunderInternalURL(org, env) + "/oauth2/token"
}

// ThunderExternalTokenURL returns the public OAuth2 token endpoint for the env-Thunder instance.
// Reachable from outside the cluster via the HTTPRoute that maps the handle -> the
// Thunder service (locally through the k3d gateway; on a VM through the Caddy
// wildcard site — see deployments/vm/lib-vm.sh).
// Use this in developer-facing API responses (console copy-buttons, Identity page, etc.).
// See thunderExternalOrigin for handle.
func ThunderExternalTokenURL(handle string) string {
	return thunderExternalOrigin(handle) + "/oauth2/token"
}

// ThunderExternalJWKSURL returns the public JWKS endpoint for the env-Thunder instance.
// Reachable from outside the cluster via the same route as ThunderExternalTokenURL.
// Use this in developer-facing API responses. See thunderExternalOrigin for handle.
func ThunderExternalJWKSURL(handle string) string {
	return thunderExternalOrigin(handle) + "/oauth2/jwks"
}

// thunderExternalOrigin returns "<scheme>://<handle>.<baseDomain>[:8080]" — the
// externally reachable origin for an env-Thunder instance, addressed by its
// EnvThunderURL handle (see that model's doc comment for why org/env are never
// used here: every environment is assigned a handle — user-chosen or generated —
// at provisioning time, so there is nothing else this could be built from).
// baseDomain is deployment-specific (THUNDER_HOST_BASE_DOMAIN, e.g. "amp.localhost"
// for local dev or a VM's own real domain) — the handle sits directly under it,
// with no fixed subdomain segment in between, so a VM's domain shape is honored
// exactly as configured. reservedThunderHandles (environment_service.go) blocks
// handles that would collide with the platform's own fixed subdomains (console,
// api, thunder, etc.) under the same baseDomain.
// Local dev (config.TLSConfig.EnableTLS == false, the default) reaches env-Thunder
// directly on the k3d gateway's plain-HTTP port 8080. VM/production deployments
// (TLS_ENABLED=true — the same flag deployments/vm/lib-vm.sh already sets for
// platform Thunder's own advertised URLs) front env-Thunder with Caddy on the
// standard HTTPS port instead, so no port is appended:
// deployments/scripts/thunder-naming.sh's thunder_issuer() must produce the SAME
// scheme/port shape when TLS_ENABLED=true, or the URL reported here won't match
// what env-Thunder itself self-configured as its issuer/publicUrl.
func thunderExternalOrigin(handle string) string {
	if handle == "" {
		panic("thunder url handle must not be empty — callers must check for \"not provisioned\" before building a URL")
	}
	host := fmt.Sprintf("%s.%s", handle, config.GetConfig().ThunderHostBaseDomain)
	if config.GetConfig().TLSConfig.EnableTLS {
		return fmt.Sprintf("https://%s", host)
	}
	return fmt.Sprintf("http://%s:8080", host)
}

// ThunderIssuerURL returns the public issuer URL for the env-Thunder instance —
// what Thunder stamps into the JWT iss claim. See thunderExternalOrigin for handle.
func ThunderIssuerURL(handle string) string {
	return thunderExternalOrigin(handle)
}

// isValidJWKS reports whether body is a syntactically valid JWKS document (a JSON
// object with a non-empty "keys" array). An HTTP 200 alone is not sufficient evidence
// of a live Thunder instance — any unrelated server answering on a probed
// address/port (e.g. a stray dev server bound to the same fallback address) would
// otherwise be misreported as a reachable env-Thunder.
func isValidJWKS(body []byte) bool {
	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return false
	}
	return len(doc.Keys) > 0
}

// thunderURLCandidate pairs a candidate base URL for an env-Thunder instance with
// the actual host:port a caller should connect to in order to reach it. resolveToHost
// is empty when the base URL's own host is directly dialable; when set, callers must
// dial resolveToHost while keeping the base URL's host as the HTTP Host header, so
// Kgateway's host-based routing still selects the right backend.
type thunderURLCandidate struct {
	baseURL       string
	resolveToHost string
}

// thunderBaseURLCandidates returns, in preference order, every base URL an env-Thunder
// instance might be reachable at: cluster-internal DNS (the real address in any
// Kubernetes deployment) and the public ingress hostname are always tried. Two more,
// host-header-resolved fallbacks targeting fixed local addresses (Docker Desktop's
// host.docker.internal, then plain Linux host networking) are appended ONLY when
// config.IsLocalDevEnv is set: they exist purely to compensate for
// agent-manager-service running as a plain Docker container (docker-compose) rather
// than a Kubernetes pod in local dev, where neither of the two real candidates can be
// resolved. They must never be tried outside that context — in-cluster, the
// cluster-internal candidate always succeeds when Thunder is actually reachable, and
// probing 127.0.0.1 there would hit agent-manager-service's own pod rather than
// Thunder, which is both pointless and a latent false-positive risk if anything ever
// answers on that port. This is the single source of truth for the candidate cascade —
// both ThunderProbe and ResolveThunderBaseURL (used by EnvThunderResolver to reach
// env-Thunder's admin API for per-agent client provisioning) build their attempts from
// this same list, so the two can never drift out of sync with each other.
//
// handle builds the external candidate (see thunderExternalOrigin — it must be
// non-empty; callers check for "not provisioned" before ever reaching here). The
// internal cluster-DNS candidate is unaffected — it's addressed by org/env, not
// part of the guessable attack surface handle replaces (see EnvThunderURL's doc
// comment).
func thunderBaseURLCandidates(org, env, handle string) []thunderURLCandidate {
	externalBaseURL := thunderExternalOrigin(handle)
	candidates := []thunderURLCandidate{
		{baseURL: ThunderInternalURL(org, env)},
		{baseURL: externalBaseURL},
	}
	if config.GetConfig().IsLocalDevEnv {
		candidates = append(
			candidates,
			thunderURLCandidate{baseURL: externalBaseURL, resolveToHost: "host.docker.internal:8080"},
			thunderURLCandidate{baseURL: externalBaseURL, resolveToHost: "127.0.0.1:8080"},
		)
	}
	return candidates
}

// probeThunderURL reports whether a GET to url succeeds AND its body is a valid JWKS
// document (see isValidJWKS) — a bare HTTP 200 is not sufficient evidence of a live
// Thunder instance, since an unrelated server answering on a probed fallback address
// would otherwise be misreported as reachable. Optionally dials resolveToHost instead
// of the URL's own host (see thunderURLCandidate).
func probeThunderURL(ctx context.Context, url, resolveToHost string) bool {
	const probeTimeout = 2 * time.Second
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	if resolveToHost != "" {
		req.Host = req.URL.Host
		req.URL.Host = resolveToHost
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSProbeBodyBytes))
	if err != nil {
		return false
	}
	return isValidJWKS(body)
}

// thunderURLProber checks whether a candidate base URL is reachable — its JWKS
// endpoint is used as the health check since it requires no authentication.
// Injectable so callers can test the candidate-selection cascade without real
// network access.
type thunderURLProber func(ctx context.Context, candidate thunderURLCandidate) bool

// defaultThunderURLProber is the real, network-probing implementation used outside tests.
func defaultThunderURLProber(ctx context.Context, candidate thunderURLCandidate) bool {
	return probeThunderURL(ctx, candidate.baseURL+"/oauth2/jwks", candidate.resolveToHost)
}

// resolveThunderBaseURL returns the first candidate base URL that prober reports as
// reachable, trying them in thunderBaseURLCandidates' preference order. ok is false
// if none respond.
func resolveThunderBaseURL(ctx context.Context, org, env, handle string, prober thunderURLProber) (candidate thunderURLCandidate, ok bool) {
	for _, c := range thunderBaseURLCandidates(org, env, handle) {
		if prober(ctx, c) {
			return c, true
		}
	}
	return thunderURLCandidate{}, false
}

// ResolveThunderBaseURL returns the first reachable base URL for an env-Thunder
// instance, trying cluster-internal DNS, the external ingress hostname, then (in local
// dev only) Docker Desktop/Linux host-networking fallbacks. Callers that build an HTTP
// client against the result must dial resolveToHost (when non-empty) instead of the
// base URL's own host, while still sending the base URL's host as the Host header.
// See ThunderExternalTokenURL for handle.
func ResolveThunderBaseURL(ctx context.Context, org, env, handle string) (baseURL, resolveToHost string, ok bool) {
	c, ok := resolveThunderBaseURL(ctx, org, env, handle, defaultThunderURLProber)
	return c.baseURL, c.resolveToHost, ok
}

// ThunderProbe checks whether an env-Thunder instance is reachable by trying the same
// candidate cascade as ResolveThunderBaseURL (each candidate's JWKS endpoint, validated
// as an actual JWKS document via isValidJWKS — not just an HTTP 200). All candidates
// are probed CONCURRENTLY, not one after another, so latency is bounded by a single
// probe's 2-second timeout rather than the sum of however many are tried. Callers treat
// a negative probe as "not provisioned" and skip the env. See ThunderExternalTokenURL
// for handle.
func ThunderProbe(ctx context.Context, org, env, handle string) bool {
	candidates := thunderBaseURLCandidates(org, env, handle)

	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan bool, len(candidates))
	for _, c := range candidates {
		go func(c thunderURLCandidate) {
			results <- probeThunderURL(probeCtx, c.baseURL+"/oauth2/jwks", c.resolveToHost)
		}(c)
	}

	for range candidates {
		if <-results {
			return true
		}
	}
	return false
}

const (
	maxAgentAppNameLen = 100
	// agentAppNameTruncatePrefixLen leaves room for "-" + a 6-char thunderSHA6
	// suffix (7 chars) within maxAgentAppNameLen, mirroring ThunderReleaseName's
	// truncation scheme above.
	agentAppNameTruncatePrefixLen = maxAgentAppNameLen - 7
)

// AgentThunderAppName returns the OAuth app name to use in Thunder for a per-agent client.
// Format: amp-agent-<org>-<env>-<project>-<agent>, truncated to 100 chars with a
// collision-safe hash suffix (see ThunderReleaseName). The name mirrors
// amp-publisher-<org> but is fully scoped to env + project + agent to avoid
// collisions.
//
// env is included even though each env already has its own physically separate
// Thunder instance (so it isn't needed for uniqueness there): without it, every
// env-Thunder's agent list looks identical — e.g. "amp-agent-default-default-x" in
// both the "stage" and "testing" instances — with nothing in the name itself
// showing which environment you're looking at from inside Thunder's own console.
func AgentThunderAppName(org, env, project, agent string) string {
	org = slugify(org)
	env = slugify(env)
	project = slugify(project)
	agent = slugify(agent)
	if org == "" || env == "" || project == "" || agent == "" {
		panic("org, env, project, and agent names must be valid alphanumeric slugs and not empty")
	}
	name := fmt.Sprintf("amp-agent-%s-%s-%s-%s", org, env, project, agent)
	if len(name) <= maxAgentAppNameLen {
		return strings.TrimSuffix(name, "-")
	}
	hash := thunderSHA6(org + "/" + env + "/" + project + "/" + agent)
	prefix := strings.TrimRight(name[:agentAppNameTruncatePrefixLen], "-")
	return fmt.Sprintf("%s-%s", prefix, hash)
}

// thunderSHA6 returns the first 6 hex characters of the SHA-256 hash of s.
// Produces the same output as _sha6() in deployments/scripts/thunder-naming.sh.
func thunderSHA6(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:])[:6]
}

// slugify converts string to lowercase, replaces invalid characters (spaces, underscores)
// with hyphens, merges consecutive hyphens, and trims leading/trailing hyphens.
func slugify(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else if r == '-' || r == '_' || r == ' ' {
			sb.WriteRune('-')
		}
	}
	res := sb.String()
	for strings.Contains(res, "--") {
		res = strings.ReplaceAll(res, "--", "-")
	}
	return strings.Trim(res, "-")
}
