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
# (-- so needles starting with '-' aren't parsed as grep options)
has() { grep -qF -- "$2" <<<"$1" && echo yes || echo no; }

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

# --- build_gateway_helm_args sets the published vhost + user-token keymanager issuer ---
gw="$(build_gateway_helm_args 203.0.113.10 https)"
assert_eq "gateway vhost" \
  "gateway.vhost=https://gateway.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'gateway.vhost' <<<"$gw")"
# keymanagers supplied as a full list via --set-json (a list-index --set wipes the
# other entry); ThunderKeyManager gets the public issuer, agent-manager-service kept.
assert_eq "gateway keymanagers via --set-json" "yes" "$(has "$gw" '--set-json')"
km_json="$(grep -F 'keymanagers=' <<<"$gw")"
assert_eq "gateway keymanagers is a full list" "yes" "$(has "$km_json" 'keymanagers=[{')"
assert_eq "gateway keeps agent-manager-service km" "yes" "$(has "$km_json" '"name":"agent-manager-service"')"
assert_eq "gateway ThunderKeyManager public issuer" "yes" \
  "$(has "$km_json" '"name":"ThunderKeyManager","issuer":"https://thunder.amp.203.0.113.10.sslip.io"')"
assert_eq "gateway no sparse/null keymanager" "no" "$(has "$km_json" 'null')"

# --- build_observability_helm_args points the traces observer at the public issuer ---
obs="$(build_observability_helm_args 203.0.113.10 https)"
assert_eq "observability traces issuer -> public thunder" \
  "tracesObserver.auth.issuer=https://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'tracesObserver.auth.issuer' <<<"$obs")"
assert_eq "observability http scheme" \
  "tracesObserver.auth.issuer=http://thunder.amp.203.0.113.10.sslip.io" \
  "$(grep -F 'tracesObserver.auth.issuer' <<<"$(build_observability_helm_args 203.0.113.10 http)")"

# --- render_dataplane_external_ingress: public host on :443, both http+https entries
#     bound to the internal http listener (console reads the http variant; CSP upgrades it) ---
dpe="$(render_dataplane_external_ingress 203.0.113.10 https)"
assert_eq "dp external public host"    "yes" "$(has "$dpe" 'host: "agents.203.0.113.10.sslip.io"')"
assert_eq "dp external port 443"       "yes" "$(has "$dpe" 'port: 443')"
assert_eq "dp external listener http"  "yes" "$(has "$dpe" 'listenerName: http')"
assert_eq "dp external has http entry" "yes" "$(printf '%s\n' "$dpe" | grep -qE '^        http:' && echo yes || echo no)"
assert_eq "dp external has https entry" "yes" "$(printf '%s\n' "$dpe" | grep -qE '^        https:' && echo yes || echo no)"
assert_eq "dp external no port 19080/19443 (not local default)" "no" "$(has "$dpe" 'port: 1908')"
assert_eq "dp external http mode port 80" "yes" "$(has "$(render_dataplane_external_ingress 203.0.113.10 http)" 'port: 80')"

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
# redirectUri emitted under both setup (legacy) and bootstrap (>=0.15.0) keys
assert_eq "thunder console redirectUri (bootstrap key)" \
  "thunder.bootstrap.ampConsoleClient.redirectUris[0]=https://console.amp.203.0.113.10.sslip.io/login" \
  "$(grep -F 'thunder.bootstrap.ampConsoleClient.redirectUris' <<<"$th")"
assert_eq "thunder console redirectUri (legacy setup key)" \
  "thunder.setup.ampConsoleClient.redirectUris[0]=https://console.amp.203.0.113.10.sslip.io/login" \
  "$(grep -F 'thunder.setup.ampConsoleClient.redirectUris' <<<"$th")"

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
# registry mirror endpoint -> node host (so node containerd can pull); key untouched
reg_in="$(printf '%s\n' \
  '    mirrors:' \
  '      "host.k3d.internal:10082":' \
  '        endpoint:' \
  '          - http://host.k3d.internal:10082')"
reg_out="$(render_k3d_vm_config <<<"$reg_in")"
assert_eq "k3d registry endpoint -> node host" \
  "          - http://k3d-amp-local-server-0:10082" \
  "$(grep -F 'endpoint' -A1 <<<"$reg_out" | grep -F 'http://')"
assert_eq "k3d registry mirror key untouched" \
  '      "host.k3d.internal:10082":' \
  "$(grep -F '"host.k3d.internal:10082":' <<<"$reg_out")"

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
assert_eq "caddy http console site" "http://console.amp.203.0.113.10.sslip.io {" "$(grep -F 'http://console.amp.203.0.113.10.sslip.io {' <<<"$cf_http")"
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
# per-site tls block appears on each public host incl. cp (6) + the agent wildcard (1) = 7
assert_eq "np80 tls block per site (7)"     "7"   "$(grep -cF 'issuer acme' <<<"$cf_np80")"
# default (no_port80 omitted) must NOT add either directive
cf_def="$(render_caddyfile 203.0.113.10 https "ops@example.com" true)"
assert_eq "default no disable_redirects"    "no"  "$(has "$cf_def" 'auto_https disable_redirects')"
assert_eq "default no http-challenge block" "no"  "$(has "$cf_def" 'disable_http_challenge')"

# --- build_platform_resources_helm_args points the workload publisher at the
#     Thunder service directly (the gateway path 404s once Thunder's vhost moves
#     to the public sslip.io host) ---
pr="$(build_platform_resources_helm_args)"
assert_eq "platform-resources oauth tokenUrl (direct svc)" \
  "global.oauth.tokenUrl=http://amp-thunder-extension-service.amp-thunder.svc.cluster.local:8090/oauth2/token" \
  "$(grep -F 'global.oauth.tokenUrl' <<<"$pr")"
assert_eq "platform-resources oauth not via host.k3d.internal" "no" "$(has "$pr" 'host.k3d.internal')"

# --- render_coredns_vm_config rewrites the in-cluster names to the server node ---
cd_cfg="$(render_coredns_vm_config k3d-amp-local-server-0)"
assert_eq "coredns configmap name" "yes" "$(has "$cd_cfg" 'name: coredns-custom')"
assert_eq "coredns openchoreo -> node" "yes" \
  "$(has "$cd_cfg" 'name regex (.+\.)?openchoreo\.localhost k3d-amp-local-server-0')"
assert_eq "coredns amp -> node" "yes" \
  "$(has "$cd_cfg" 'name regex (.+\.)?amp\.localhost k3d-amp-local-server-0')"
assert_eq "coredns host aliases -> node" "yes" \
  "$(has "$cd_cfg" 'name regex (host\.k3d\.internal|host\.docker\.internal) k3d-amp-local-server-0')"
assert_eq "coredns no longer targets host.k3d.internal as dest" "no" \
  "$(has "$cd_cfg" 'localhost host.k3d.internal')"

# --- render_caddyfile: deployed-agent invocation (CSP on console, wildcard site,
#     on-demand TLS, CORS, ask endpoint) ---
cf_ai="$(render_caddyfile 203.0.113.10 https "ops@example.com" true false)"
assert_eq "console CSP upgrade-insecure-requests" "yes" \
  "$(has "$cf_ai" 'header Content-Security-Policy "upgrade-insecure-requests"')"
assert_eq "global on_demand_tls ask" "yes" "$(has "$cf_ai" 'ask http://127.0.0.1:9753')"
assert_eq "on-demand ask endpoint site" "yes" "$(has "$cf_ai" 'http://127.0.0.1:9753 {')"
assert_eq "wildcard agent site" "yes" "$(has "$cf_ai" '*.agents.203.0.113.10.sslip.io {')"
assert_eq "agent site on_demand tls" "yes" "$(has "$cf_ai" 'on_demand')"
assert_eq "agent site proxies data-plane gw" "yes" "$(has "$cf_ai" 'reverse_proxy 127.0.0.1:19080')"
assert_eq "agent CORS allow-origin = console" "yes" \
  "$(has "$cf_ai" 'Access-Control-Allow-Origin "https://console.amp.203.0.113.10.sslip.io"')"
assert_eq "agent CORS allows X-API-Key" "yes" "$(has "$cf_ai" 'Authorization, Content-Type, X-API-Key')"
assert_eq "agent CORS preflight short-circuit" "yes" "$(has "$cf_ai" 'respond @cors_preflight 204')"
# no-port-80: agent site forces TLS-ALPN-01 alongside on_demand
cf_ai80="$(render_caddyfile 203.0.113.10 https "" true true)"
assert_eq "agent site on_demand + disable_http_challenge (np80)" "yes" \
  "$(printf '%s' "$cf_ai80" | awk '/\*\.agents\./{f=1} f' | grep -qF 'disable_http_challenge' && echo yes || echo no)"
# http mode: no CSP, no on-demand, but still a wildcard agent site with CORS
cf_aihttp="$(render_caddyfile 203.0.113.10 http "" true false)"
assert_eq "http mode no CSP"        "no"  "$(has "$cf_aihttp" 'Content-Security-Policy')"
assert_eq "http mode no on_demand"  "no"  "$(has "$cf_aihttp" 'on_demand')"
assert_eq "http mode agent site"    "yes" "$(has "$cf_aihttp" 'http://*.agents.203.0.113.10.sslip.io {')"
assert_eq "http mode agent CORS origin http" "yes" \
  "$(has "$cf_aihttp" 'Access-Control-Allow-Origin "http://console.amp.203.0.113.10.sslip.io"')"

if [[ "$FAILED" -ne 0 ]]; then echo "TESTS FAILED"; exit 1; fi
echo "ALL TESTS PASSED"
