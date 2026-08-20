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
	"strings"
	"testing"
	"time"

	"github.com/wso2/agent-manager/agent-manager-service/config"
)

// TestThunderExternalURLs_RespectBaseDomainConfig locks in that a VM deployment's
// THUNDER_HOST_BASE_DOMAIN override flows straight through into every URL builder —
// this is what makes deployments/vm/lib-vm.sh setting the same value (both for
// add-environment-thunder.sh and this Go config) keep the reported URLs and the
// actually-deployed Thunder instance's own issuer in sync.
func TestThunderExternalURLs_RespectBaseDomainConfig(t *testing.T) {
	orig := config.GetConfig().ThunderHostBaseDomain
	defer func() { config.GetConfig().ThunderHostBaseDomain = orig }()

	config.GetConfig().ThunderHostBaseDomain = "amp.203.0.113.10.sslip.io"
	got := ThunderIssuerURL("x7f2q9kz")
	want := "http://x7f2q9kz.amp.203.0.113.10.sslip.io:8080"
	if got != want {
		t.Errorf("ThunderIssuerURL with overridden base domain: want %q, got %q", want, got)
	}
}

// TestThunderExternalURLs_RespectTLSConfig locks in that TLS_ENABLED (the same flag
// deployments/vm/lib-vm.sh already sets for platform Thunder's own advertised URLs)
// switches env-Thunder's reported scheme AND drops the :8080 suffix — matching Caddy
// terminating on the standard HTTPS port on a VM, rather than the k3d gateway's
// plain-HTTP :8080 in local dev.
func TestThunderExternalURLs_RespectTLSConfig(t *testing.T) {
	origDomain := config.GetConfig().ThunderHostBaseDomain
	origTLS := config.GetConfig().TLSConfig.EnableTLS
	defer func() {
		config.GetConfig().ThunderHostBaseDomain = origDomain
		config.GetConfig().TLSConfig.EnableTLS = origTLS
	}()
	config.GetConfig().ThunderHostBaseDomain = "amp.203.0.113.10.sslip.io"

	config.GetConfig().TLSConfig.EnableTLS = false
	if got, want := ThunderIssuerURL("x7f2q9kz"), "http://x7f2q9kz.amp.203.0.113.10.sslip.io:8080"; got != want {
		t.Errorf("ThunderIssuerURL (TLS off): want %q, got %q", want, got)
	}
	if got, want := ThunderExternalJWKSURL("x7f2q9kz"), "http://x7f2q9kz.amp.203.0.113.10.sslip.io:8080/oauth2/jwks"; got != want {
		t.Errorf("ThunderExternalJWKSURL (TLS off): want %q, got %q", want, got)
	}

	config.GetConfig().TLSConfig.EnableTLS = true
	if got, want := ThunderIssuerURL("x7f2q9kz"), "https://x7f2q9kz.amp.203.0.113.10.sslip.io"; got != want {
		t.Errorf("ThunderIssuerURL (TLS on): want %q, got %q", want, got)
	}
	if got, want := ThunderExternalTokenURL("x7f2q9kz"), "https://x7f2q9kz.amp.203.0.113.10.sslip.io/oauth2/token"; got != want {
		t.Errorf("ThunderExternalTokenURL (TLS on): want %q, got %q", want, got)
	}
}

// TestThunderIssuerURL_PanicsOnEmptyHandle guards the fail-loud contract: a
// caller must check for "not provisioned" (an environment with no registered
// handle) BEFORE building a URL — thunderExternalOrigin takes only a handle,
// with no org/env fallback to degrade to, so an empty handle panics instead
// of producing a guessable address.
func TestThunderIssuerURL_PanicsOnEmptyHandle(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error(`expected ThunderIssuerURL("") to panic`)
		}
	}()
	ThunderIssuerURL("")
}

// TestThunderReleaseName_NoHyphenCollapsing locks in that ThunderReleaseName does
// NOT collapse consecutive hyphens, matching add-environment-thunder.sh exactly.
// It validates ENV_NAME against ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ (which permits
// internal "--") and uses org/env raw — never slugify/collapse. If this Go code
// collapsed "--" to "-" (as slugify() does), it would compute a different release
// name than what actually gets deployed whenever an org or env name contains a
// double hyphen, causing AMS's own admin-API calls to Thunder
// (ThunderInternalURL/ThunderTokenURL, used for per-agent client provisioning) to
// target an address that doesn't exist.
func TestThunderReleaseName_NoHyphenCollapsing(t *testing.T) {
	got := ThunderReleaseName("my--org", "env")
	want := "amp-thunder-my--org-env"
	if got != want {
		t.Errorf("ThunderReleaseName must not collapse consecutive hyphens: want %q, got %q", want, got)
	}
}

func TestThunderReleaseName_Lowercases(t *testing.T) {
	got := ThunderReleaseName("MyOrg", "Staging")
	want := "amp-thunder-myorg-staging"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestThunderReleaseName_BasicCases(t *testing.T) {
	cases := []struct {
		org, env, want string
	}{
		{"default", "default", "amp-thunder-default-default"},
		{"my-org", "staging", "amp-thunder-my-org-staging"},
	}
	for _, tc := range cases {
		got := ThunderReleaseName(tc.org, tc.env)
		if got != tc.want {
			t.Errorf("ThunderReleaseName(%q, %q): want %q, got %q", tc.org, tc.env, tc.want, got)
		}
	}
}

// TestAgentThunderAppName_IncludesEnv locks in that the Thunder app name embeds
// the environment name — without it, every env-Thunder's own agent list looks
// identical (e.g. "amp-agent-default-default-x" in both "stage" and "testing"),
// with nothing in the name itself showing which environment an operator browsing
// Thunder's console directly is actually looking at.
func TestAgentThunderAppName_IncludesEnv(t *testing.T) {
	cases := []struct {
		org, env, project, agent, want string
	}{
		{"default", "stage", "default", "my-agent", "amp-agent-default-stage-default-my-agent"},
		{"acme", "production", "proj1", "agent-1", "amp-agent-acme-production-proj1-agent-1"},
	}
	for _, tc := range cases {
		got := AgentThunderAppName(tc.org, tc.env, tc.project, tc.agent)
		if got != tc.want {
			t.Errorf("AgentThunderAppName(%q, %q, %q, %q): want %q, got %q", tc.org, tc.env, tc.project, tc.agent, tc.want, got)
		}
	}
}

func TestAgentThunderAppName_DifferentEnvsProduceDifferentNames(t *testing.T) {
	stage := AgentThunderAppName("default", "stage", "default", "my-agent")
	testing_ := AgentThunderAppName("default", "testing", "default", "my-agent")
	if stage == testing_ {
		t.Errorf("expected different environments to produce different app names, both were %q", stage)
	}
}

func TestAgentThunderAppName_TruncatesAt100Chars(t *testing.T) {
	got := AgentThunderAppName(
		strings.Repeat("o", 40), strings.Repeat("e", 40), strings.Repeat("p", 40), strings.Repeat("a", 40),
	)
	if len(got) > 100 {
		t.Errorf("expected app name truncated to 100 chars, got %d chars: %q", len(got), got)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("expected no trailing hyphen after truncation, got %q", got)
	}
}

// TestAgentThunderAppName_TruncationDoesNotCollide guards against two distinct
// agents producing the identical Thunder app name: without a collision-avoidance
// hash, two names sharing the same first 100 characters (and differing only
// beyond that cutoff) would truncate to the exact same string, causing Thunder's
// 409 fallback (findAgentByName) to hand the second agent's binding the first
// agent's Thunder identity — so regenerating or revoking one silently rotates or
// kills the other's credential.
func TestAgentThunderAppName_TruncationDoesNotCollide(t *testing.T) {
	sharedPrefix := strings.Repeat("x", 200)
	agentA := sharedPrefix + "aaa"
	agentB := sharedPrefix + "bbb"

	nameA := AgentThunderAppName("acme", "staging", "proj1", agentA)
	nameB := AgentThunderAppName("acme", "staging", "proj1", agentB)

	if nameA == nameB {
		t.Fatalf("two distinct agents truncated to the same Thunder app name: %q", nameA)
	}
	if len(nameA) > 100 || len(nameB) > 100 {
		t.Errorf("truncated names must still respect the 100-char limit: %d, %d", len(nameA), len(nameB))
	}
}

// These lock in resolveThunderBaseURL's candidate cascade — the mechanism that lets
// AMS reach env-Thunder both when running in-cluster (production) and when running
// via docker-compose outside the cluster (local dev), where *.svc.cluster.local
// cannot be resolved at all. A fake prober stands in for real network probing so
// the cascade order and short-circuiting are tested deterministically.

func TestResolveThunderBaseURL_PrefersClusterInternal(t *testing.T) {
	var probed []thunderURLCandidate
	prober := func(_ context.Context, c thunderURLCandidate) bool {
		probed = append(probed, c)
		return c.baseURL == ThunderInternalURL("acme", "staging")
	}

	got, ok := resolveThunderBaseURL(context.Background(), "acme", "staging", "x7f2q9kz", prober)
	if !ok {
		t.Fatal("expected ok=true when the cluster-internal candidate is reachable")
	}
	if got.baseURL != ThunderInternalURL("acme", "staging") {
		t.Errorf("want cluster-internal base URL, got %q", got.baseURL)
	}
	if got.resolveToHost != "" {
		t.Errorf("cluster-internal candidate must not set resolveToHost, got %q", got.resolveToHost)
	}
	if len(probed) != 1 {
		t.Errorf("must stop at the first reachable candidate, probed %d", len(probed))
	}
}

func TestResolveThunderBaseURL_FallsBackToExternalIngress(t *testing.T) {
	externalBaseURL := "http://x7f2q9kz.amp.localhost:8080"
	prober := func(_ context.Context, c thunderURLCandidate) bool {
		return c.baseURL == externalBaseURL && c.resolveToHost == ""
	}

	got, ok := resolveThunderBaseURL(context.Background(), "acme", "staging", "x7f2q9kz", prober)
	if !ok {
		t.Fatal("expected ok=true when the external ingress candidate is reachable")
	}
	if got.baseURL != externalBaseURL || got.resolveToHost != "" {
		t.Errorf("want external ingress candidate with no dial override, got %+v", got)
	}
}

// TestResolveThunderBaseURL_ExternalIngressCandidate_RespectsTLSConfig guards a
// regression: the external ingress candidate must match thunderExternalOrigin's
// TLS-aware scheme/port (what env-Thunder itself advertises as its issuer/JWKS),
// not a hardcoded http://...:8080 — otherwise a VM/production deployment
// (TLS_ENABLED=true) falling back to this candidate probes the wrong scheme and
// port entirely, and reports a live env-Thunder as unreachable.
func TestResolveThunderBaseURL_ExternalIngressCandidate_RespectsTLSConfig(t *testing.T) {
	origTLS := config.GetConfig().TLSConfig.EnableTLS
	defer func() { config.GetConfig().TLSConfig.EnableTLS = origTLS }()
	config.GetConfig().TLSConfig.EnableTLS = true

	wantBaseURL := "https://x7f2q9kz.amp.localhost"
	prober := func(_ context.Context, c thunderURLCandidate) bool {
		return c.baseURL == wantBaseURL && c.resolveToHost == ""
	}

	got, ok := resolveThunderBaseURL(context.Background(), "acme", "staging", "x7f2q9kz", prober)
	if !ok {
		t.Fatal("expected ok=true when the TLS-aware external ingress candidate is reachable")
	}
	if got.baseURL != wantBaseURL || got.resolveToHost != "" {
		t.Errorf("want TLS-aware external ingress candidate %q, got %+v", wantBaseURL, got)
	}
}

// The Docker Desktop / loopback candidates are only ever generated when
// config.IsLocalDevEnv is set (see thunderBaseURLCandidates) — they exist purely to
// compensate for agent-manager-service running as a plain Docker container in local
// dev, and must never be probed in-cluster. Both tests below flip that flag for their
// duration to exercise the fallback path.

func TestResolveThunderBaseURL_FallsBackToDockerDesktop(t *testing.T) {
	orig := config.GetConfig().IsLocalDevEnv
	defer func() { config.GetConfig().IsLocalDevEnv = orig }()
	config.GetConfig().IsLocalDevEnv = true

	prober := func(_ context.Context, c thunderURLCandidate) bool {
		return c.resolveToHost == "host.docker.internal:8080"
	}

	got, ok := resolveThunderBaseURL(context.Background(), "acme", "staging", "x7f2q9kz", prober)
	if !ok {
		t.Fatal("expected ok=true when only the host.docker.internal candidate is reachable")
	}
	if got.resolveToHost != "host.docker.internal:8080" {
		t.Errorf("want host.docker.internal dial override, got %+v", got)
	}
}

func TestResolveThunderBaseURL_FallsBackToLoopback(t *testing.T) {
	orig := config.GetConfig().IsLocalDevEnv
	defer func() { config.GetConfig().IsLocalDevEnv = orig }()
	config.GetConfig().IsLocalDevEnv = true

	prober := func(_ context.Context, c thunderURLCandidate) bool {
		return c.resolveToHost == "127.0.0.1:8080"
	}

	got, ok := resolveThunderBaseURL(context.Background(), "acme", "staging", "x7f2q9kz", prober)
	if !ok {
		t.Fatal("expected ok=true when only the 127.0.0.1 candidate is reachable")
	}
	if got.resolveToHost != "127.0.0.1:8080" {
		t.Errorf("want 127.0.0.1 dial override, got %+v", got)
	}
}

func TestResolveThunderBaseURL_AllUnreachable(t *testing.T) {
	prober := func(_ context.Context, _ thunderURLCandidate) bool { return false }

	_, ok := resolveThunderBaseURL(context.Background(), "acme", "staging", "x7f2q9kz", prober)
	if ok {
		t.Error("expected ok=false when no candidate is reachable")
	}
}

func TestResolveThunderBaseURL_PublicWrapperUsesRealCascadeShape(t *testing.T) {
	// ResolveThunderBaseURL can't be probed against real network in a unit test,
	// but it must at least be wired to the real candidate cascade for a
	// definitely-unreachable org/env, rather than e.g. always returning ok=true.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, ok := ResolveThunderBaseURL(ctx, "nonexistent-org-xyz", "nonexistent-env-xyz", "nonexistent-handle-xyz")
	if ok {
		t.Error("expected ok=false for an org/env with no env-Thunder deployed anywhere reachable")
	}
}

// isValidJWKS is ThunderProbe's correctness check on top of an HTTP 200 — these cases
// lock in that a bare 200 with an unrelated body (e.g. from a stray server on a
// probed fallback address) is NOT treated as a live env-Thunder.
func TestIsValidJWKS(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"real JWKS with one key", `{"keys":[{"kty":"RSA","kid":"abc","use":"sig"}]}`, true},
		{"real JWKS with multiple keys", `{"keys":[{"kty":"RSA"},{"kty":"EC"}]}`, true},
		{"empty keys array", `{"keys":[]}`, false},
		{"missing keys field", `{"foo":"bar"}`, false},
		{"not JSON", `<html>404 not found</html>`, false},
		{"empty body", ``, false},
		{"keys is not an array", `{"keys":"not-an-array"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isValidJWKS([]byte(tc.body))
			if got != tc.want {
				t.Errorf("isValidJWKS(%q): want %v, got %v", tc.body, tc.want, got)
			}
		})
	}
}
