#!/usr/bin/env bash
# Unit tests for lib-vm.sh. Run: bash deployments/quick-start/vm/tests/run.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib-vm.sh disable=SC1091
source "${SCRIPT_DIR}/../lib-vm.sh"

FAILED=0
assert_eq() {
  local label="$1" expected="$2" actual="$3"
  if [[ "$expected" == "$actual" ]]; then
    printf 'ok   - %s\n' "$label"
  else
    printf 'FAIL - %s\n      expected: %q\n      actual:   %q\n' "$label" "$expected" "$actual"
    FAILED=1
  fi
}
# has <haystack> <needle> -> "yes" if needle present, else "no"
has() { grep -qF "$2" <<<"$1" && echo yes || echo no; }

# --- vm_scheme ---
assert_eq "vm_scheme letsencrypt" "https" "$(vm_scheme letsencrypt)"
assert_eq "vm_scheme http"        "http"  "$(vm_scheme http)"
{ vm_scheme badmode >/dev/null 2>&1; } ; assert_eq "vm_scheme unknown returns 1" "1" "$?"

# --- vm_host ---
assert_eq "vm_host console" "console.amp.203.0.113.10.sslip.io" "$(vm_host console 203.0.113.10)"
assert_eq "vm_host thunder" "thunder.amp.203.0.113.10.sslip.io" "$(vm_host thunder 203.0.113.10)"

# --- build_amp_helm_args (https, external gateways on by default) ---
amp_https="$(build_amp_helm_args 203.0.113.10 https true)"
# Service settings are emitted under BOTH chart keys (agentManager + agentManagerService).
assert_eq "amp serverPublicURL (service key)" \
  "agentManagerService.config.serverPublicURL=https://api.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'agentManagerService.config.serverPublicURL' <<<"$amp_https")"
assert_eq "amp serverPublicURL (legacy key)" \
  "agentManager.config.serverPublicURL=https://api.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'agentManager.config.serverPublicURL' <<<"$amp_https")"
assert_eq "amp oauthAuthorizationServers (service key)" \
  "agentManagerService.config.oauthAuthorizationServers=https://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'agentManagerService.config.oauthAuthorizationServers' <<<"$amp_https")"
assert_eq "amp keyManager.issuer (service key)" \
  "agentManagerService.config.keyManager.issuer=https://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'agentManagerService.config.keyManager.issuer' <<<"$amp_https")"
assert_eq "amp console apiBaseUrl" \
  "console.config.apiBaseUrl=https://api.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'config.apiBaseUrl' <<<"$amp_https")"
assert_eq "amp console obsApiBaseUrl" \
  "console.config.obsApiBaseUrl=https://observer.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'obsApiBaseUrl' <<<"$amp_https")"
assert_eq "amp console instrumentationUrl" \
  "console.config.instrumentationUrl=https://gateway.amp.203.0.113.10.sslip.io/otel" \
  "$(grep -F 'instrumentationUrl' <<<"$amp_https")"
assert_eq "amp console signInRedirectURL" \
  "console.config.auth.signInRedirectURL=https://console.amp.203.0.113.10.sslip.io/login" \
  "$(grep -F 'signInRedirectURL' <<<"$amp_https")"
# external gateways on by default => full-URL gatewayControlPlaneUrl
assert_eq "amp cp url by default" \
  "console.config.gatewayControlPlaneUrl=https://cp.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'gatewayControlPlaneUrl' <<<"$amp_https")"

# --- build_amp_helm_args (https, external gateways disabled) ---
amp_nocp="$(build_amp_helm_args 203.0.113.10 https false)"
assert_eq "amp no cp when disabled" "" "$(grep -F 'gatewayControlPlaneUrl' <<<"$amp_nocp")"

# --- http mode flips scheme ---
amp_http="$(build_amp_helm_args 203.0.113.10 http true)"
assert_eq "amp http scheme" \
  "agentManagerService.config.serverPublicURL=http://api.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'agentManagerService.config.serverPublicURL' <<<"$amp_http")"

# --- build_gateway_helm_args sets the published vhost ---
gw="$(build_gateway_helm_args 203.0.113.10 https)"
assert_eq "gateway vhost" \
  "gateway.vhost=https://gateway.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'gateway.vhost' <<<"$gw")"

# --- build_cp_helm_args points OpenChoreo CP OIDC issuer at the public Thunder URL ---
cp_args="$(build_cp_helm_args 203.0.113.10 https)"
assert_eq "cp oidc issuer" \
  "security.oidc.issuer=https://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'security.oidc.issuer' <<<"$cp_args")"
assert_eq "cp oidc tokenUrl" \
  "security.oidc.tokenUrl=https://thunder.amp.203.0.113.10.sslip.io/oauth2/token" \
  "$(grep -F 'security.oidc.tokenUrl' <<<"$cp_args")"
assert_eq "cp http scheme" \
  "security.oidc.issuer=http://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'security.oidc.issuer' <<<"$(build_cp_helm_args 203.0.113.10 http)")"

# --- build_thunder_helm_args (https) ---
th="$(build_thunder_helm_args 203.0.113.10 https)"
assert_eq "thunder ocIngress.hostname" \
  "thunder.ocIngress.hostname=thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'ocIngress.hostname' <<<"$th")"
assert_eq "thunder server.publicUrl" \
  "thunder.configuration.server.publicUrl=https://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'server.publicUrl' <<<"$th")"
assert_eq "thunder jwt.issuer" \
  "thunder.configuration.jwt.issuer=https://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'jwt.issuer' <<<"$th")"
assert_eq "thunder gateClient.hostname" \
  "thunder.configuration.gateClient.hostname=thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'gateClient.hostname' <<<"$th")"
assert_eq "thunder gateClient.scheme" \
  "thunder.configuration.gateClient.scheme=https" \
  "$(grep -F 'gateClient.scheme' <<<"$th")"
assert_eq "thunder gateClient.port" \
  "thunder.configuration.gateClient.port=443" \
  "$(grep -F 'gateClient.port' <<<"$th")"
assert_eq "thunder cors origin" \
  "thunder.configuration.cors.allowedOrigins[0]=https://console.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'cors.allowedOrigins' <<<"$th")"
assert_eq "thunder console redirectUri" \
  "thunder.setup.ampConsoleClient.redirectUris[0]=https://console.amp.203.0.113.10.sslip.io/login" \
  "$(grep -F 'ampConsoleClient.redirectUris' <<<"$th")"

# --- http mode flips scheme + port ---
th_http="$(build_thunder_helm_args 203.0.113.10 http)"
assert_eq "thunder http gateClient.port" \
  "thunder.configuration.gateClient.port=80" \
  "$(grep -F 'gateClient.port' <<<"$th_http")"
assert_eq "thunder http publicUrl" \
  "thunder.configuration.server.publicUrl=http://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'server.publicUrl' <<<"$th_http")"

# --- render_k3d_vm_config ---
k3d_in="$(printf '%s\n' \
  'ports:' \
  '  - port: 3000:3000' \
  '    nodeFilters:' \
  '      - loadbalancer' \
  '  - port: 11082:9200' \
  '    nodeFilters:' \
  '      - loadbalancer')"
k3d_out="$(render_k3d_vm_config <<<"$k3d_in")"
assert_eq "k3d rebinds 3000" \
  "  - port: 127.0.0.1:3000:3000" \
  "$(grep -F '3000' <<<"$k3d_out")"
assert_eq "k3d rebinds mismatched ports" \
  "  - port: 127.0.0.1:11082:9200" \
  "$(grep -F '11082' <<<"$k3d_out")"
assert_eq "k3d leaves nodeFilters intact" \
  "    nodeFilters:" \
  "$(grep -F 'nodeFilters' <<<"$k3d_out" | head -1)"
assert_eq "k3d leaves already-bound entry untouched" \
  "  - port: 127.0.0.1:3000:3000" \
  "$(render_k3d_vm_config <<<'  - port: 127.0.0.1:3000:3000')"

# --- render_caddyfile (https, with email, external gateways disabled => no cp) ---
cf="$(render_caddyfile 203.0.113.10 https "ops@example.com" false)"
assert_eq "caddy email block" "	email ops@example.com" "$(grep -F 'email ops@example.com' <<<"$cf")"
assert_eq "caddy console site" "console.amp.203.0.113.10.sslip.io {" "$(grep -F 'console.amp' <<<"$cf" | head -1)"
assert_eq "caddy console upstream" "	reverse_proxy 127.0.0.1:3000" "$(grep -F '127.0.0.1:3000' <<<"$cf")"
assert_eq "caddy thunder upstream" "	reverse_proxy 127.0.0.1:8080" "$(grep -F '127.0.0.1:8080' <<<"$cf")"
assert_eq "caddy gateway upstream" "	reverse_proxy 127.0.0.1:22893" "$(grep -F '127.0.0.1:22893' <<<"$cf")"
assert_eq "caddy no cp when disabled" "" "$(grep -F 'cp.amp' <<<"$cf")"
assert_eq "caddy api upstream" "	reverse_proxy 127.0.0.1:9000" "$(grep -F '127.0.0.1:9000' <<<"$cf")"
assert_eq "caddy observer upstream" "	reverse_proxy 127.0.0.1:9098" "$(grep -F '127.0.0.1:9098' <<<"$cf")"

# --- http mode: http:// sites + auto_https off, no email block ---
cf_http="$(render_caddyfile 203.0.113.10 http "" false)"
assert_eq "caddy http auto_https off" "	auto_https off" "$(grep -F 'auto_https off' <<<"$cf_http")"
assert_eq "caddy http console site" "http://console.amp.203.0.113.10.sslip.io {" "$(grep -F 'http://console.amp' <<<"$cf_http")"
assert_eq "caddy http no email" "" "$(grep -F 'email' <<<"$cf_http")"

# --- external gateways on by default => cp block present (4th arg omitted) ---
cf_default="$(render_caddyfile 203.0.113.10 https "")"
assert_eq "caddy cp on by default" "cp.amp.203.0.113.10.sslip.io {" "$(grep -F 'cp.amp' <<<"$cf_default" | head -1)"

# --- explicit cp block: secure (self-signed) upstream for the 9243 control plane ---
cf_cp="$(render_caddyfile 203.0.113.10 https "" true)"
assert_eq "caddy cp site" "cp.amp.203.0.113.10.sslip.io {" "$(grep -F 'cp.amp' <<<"$cf_cp" | head -1)"
assert_eq "caddy cp tls skip verify" "			tls_insecure_skip_verify" "$(grep -F 'tls_insecure_skip_verify' <<<"$cf_cp")"

# --- no-port-80 (TLS-ALPN-01) mode: global disable_redirects + per-site disable_http_challenge ---
cf_np80="$(render_caddyfile 203.0.113.10 https "ops@example.com" true true)"
assert_eq "np80 global disable_redirects"   "yes" "$(has "$cf_np80" 'auto_https disable_redirects')"
assert_eq "np80 issuer acme"                "yes" "$(has "$cf_np80" 'issuer acme')"
assert_eq "np80 disable_http_challenge"     "yes" "$(has "$cf_np80" 'disable_http_challenge')"
assert_eq "np80 keeps email"                "yes" "$(has "$cf_np80" 'email ops@example.com')"
# per-site tls block appears on each public host incl. cp (one per site = 6)
assert_eq "np80 tls block per site (6)"     "6"   "$(grep -cF 'issuer acme' <<<"$cf_np80")"
# default (no_port80 omitted) must NOT add either directive
cf_def="$(render_caddyfile 203.0.113.10 https "ops@example.com" true)"
assert_eq "default no disable_redirects"    "no"  "$(has "$cf_def" 'auto_https disable_redirects')"
assert_eq "default no http-challenge block" "no"  "$(has "$cf_def" 'disable_http_challenge')"

if [[ "$FAILED" -ne 0 ]]; then echo "TESTS FAILED"; exit 1; fi
echo "ALL TESTS PASSED"
