#!/usr/bin/env bash
# lib-vm.sh — pure helpers for the VM standalone install.
# Sourcing this file has no side effects; every function writes only to stdout.

# vm_scheme <tls_mode> -> "https" | "http"
vm_scheme() {
  case "${1:-}" in
    letsencrypt) printf 'https' ;;
    http)        printf 'http' ;;
    *) printf 'vm_scheme: unknown tls mode: %s\n' "${1:-}" >&2; return 1 ;;
  esac
}

# vm_host <subdomain> <ip> -> "<sub>.amp.<ip>.sslip.io"
vm_host() {
  printf '%s.amp.%s.sslip.io' "$1" "$2"
}

# build_amp_helm_args <ip> <scheme> <external_gateways:true|false (default true)>
# Prints helm args, one token per line (--set and KEY=VALUE on separate lines).
# Consume with (bash >=4):  mapfile -t ARR < <(build_amp_helm_args ...)
# bash 3.2 (macOS):         while IFS= read -r l; do ARR+=("$l"); done < <(build_amp_helm_args ...)
build_amp_helm_args() {
  local ip="$1" scheme="$2" external_gateways="${3:-true}"
  local thunder api console_h observer gateway
  thunder="$(vm_host thunder "$ip")"
  api="$(vm_host api "$ip")"
  console_h="$(vm_host console "$ip")"
  observer="$(vm_host observer "$ip")"
  gateway="$(vm_host gateway "$ip")"

  # The service config lives under different top-level keys across chart versions:
  # `agentManager` (<=main) was renamed to `agentManagerService` (>=0.15.0). Emit
  # both; helm silently ignores whichever key the installed chart doesn't define,
  # so the right one always wins regardless of the --version pulled.
  local k
  for k in agentManager agentManagerService; do
    printf '%s\n' \
      "--set" "${k}.config.serverPublicURL=${scheme}://${api}" \
      "--set" "${k}.config.oauthAuthorizationServers=${scheme}://${thunder}" \
      "--set" "${k}.config.keyManager.issuer=${scheme}://${thunder}"
  done

  printf '%s\n' \
    "--set" "console.config.auth.baseUrl=${scheme}://${thunder}" \
    "--set" "console.config.auth.signInRedirectURL=${scheme}://${console_h}/login" \
    "--set" "console.config.auth.signOutRedirectURL=${scheme}://${console_h}/login" \
    "--set" "console.config.apiBaseUrl=${scheme}://${api}" \
    "--set" "console.config.obsApiBaseUrl=${scheme}://${observer}" \
    "--set" "console.config.instrumentationUrl=${scheme}://${gateway}/otel"

  if [[ "$external_gateways" == "true" ]]; then
    # Full URL: the console parses it with new URL() to build gateway setup commands.
    printf '%s\n' "--set" "console.config.gatewayControlPlaneUrl=${scheme}://$(vm_host cp "$ip")"
  fi
}

# build_gateway_helm_args <ip> <scheme>
# Prints GATEWAY_HELM_ARGS tokens. Sets the published vhost so deployed-agent
# endpoint URLs are externally reachable (path-routed under this single host).
build_gateway_helm_args() {
  local ip="$1" scheme="$2"
  printf '%s\n' "--set" "gateway.vhost=${scheme}://$(vm_host gateway "$ip")"
}

# build_cp_helm_args <ip> <scheme>
# Prints CP_HELM_ARGS tokens for the OpenChoreo control-plane install. Thunder's
# issuer is moved to the public sslip.io URL, so the OpenChoreo CP OIDC config
# (which validates the issuer string statically) must accept that same issuer —
# otherwise amp-api -> OpenChoreo calls fail with "INVALID_CLAIMS". jwksUrl /
# wellKnownEndpoint stay on the internal service (they still resolve in-cluster).
build_cp_helm_args() {
  local ip="$1" scheme="$2" thunder
  thunder="$(vm_host thunder "$ip")"
  printf '%s\n' \
    "--set" "security.oidc.issuer=${scheme}://${thunder}" \
    "--set" "security.oidc.authorizationUrl=${scheme}://${thunder}/oauth2/authorize" \
    "--set" "security.oidc.tokenUrl=${scheme}://${thunder}/oauth2/token"
}

# build_thunder_helm_args <ip> <scheme>
# Prints helm args, one token per line.
build_thunder_helm_args() {
  local ip="$1" scheme="$2"
  local thunder console_h port=443
  [[ "$scheme" == "http" ]] && port=80
  thunder="$(vm_host thunder "$ip")"
  console_h="$(vm_host console "$ip")"

  printf '%s\n' \
    "--set" "thunder.ocIngress.hostname=${thunder}" \
    "--set" "thunder.configuration.server.publicUrl=${scheme}://${thunder}" \
    "--set" "thunder.configuration.jwt.issuer=${scheme}://${thunder}" \
    "--set" "thunder.configuration.gateClient.hostname=${thunder}" \
    "--set" "thunder.configuration.gateClient.scheme=${scheme}" \
    "--set" "thunder.configuration.gateClient.port=${port}" \
    "--set" "thunder.configuration.cors.allowedOrigins[0]=${scheme}://${console_h}" \
    "--set" "thunder.setup.ampConsoleClient.redirectUris[0]=${scheme}://${console_h}/login"
}

# render_k3d_vm_config  (reads k3d config on stdin, writes loopback-bound config on stdout)
# Rewrites '- port: <host>:<container>' -> '- port: 127.0.0.1:<host>:<container>'.
# Already-bound entries (containing an IP before the first port) are left untouched.
render_k3d_vm_config() {
  sed -E 's/^([[:space:]]*- port: )([0-9]+:[0-9]+)/\1127.0.0.1:\2/'
}

# render_caddyfile <ip> <scheme> <acme_email> <external_gateways:true|false (default true)> \
#                  <no_port80:true|false (default false)>
# no_port80 (https only): use the TLS-ALPN-01 ACME challenge over :443 and drop the
# :80 redirect, so certificates issue with only inbound 443 open.
# Prints a complete Caddyfile to stdout.
render_caddyfile() {
  local ip="$1" scheme="$2" email="$3" external_gateways="${4:-true}" no_port80="${5:-false}"
  local prefix=""              # https: bare host (Caddy auto-HTTPS)
  [[ "$scheme" == "http" ]] && prefix="http://"

  # Per-site tls block injected only in no-port-80 mode: force the TLS-ALPN-01
  # ACME challenge (over :443) so issuance never depends on inbound port 80.
  local tls_block=""
  [[ "$no_port80" == "true" && "$scheme" == "https" ]] && \
    tls_block=$'\ttls {\n\t\tissuer acme {\n\t\t\tdisable_http_challenge\n\t\t}\n\t}\n'

  # Global options block.
  if [[ "$scheme" == "http" ]]; then
    printf '{\n\tauto_https off\n}\n\n'
  else
    # https: assemble global options from email + optional no-port-80 setting.
    local gopts=""
    [[ -n "$email" ]] && gopts+=$'\temail '"$email"$'\n'
    # disable_redirects stops Caddy serving the HTTP->HTTPS redirect on :80.
    [[ "$no_port80" == "true" ]] && gopts+=$'\tauto_https disable_redirects\n'
    [[ -n "$gopts" ]] && printf '{\n%s}\n\n' "$gopts"
    # https + no email + port 80 available: no global block; Caddy uses its default ACME contact.
  fi

  _caddy_site() {   # _caddy_site <prefix> <ip> <subdomain> <upstream_port> <tls_block>
    printf '%s%s {\n%s\treverse_proxy 127.0.0.1:%s\n}\n\n' "$1" "$(vm_host "$3" "$2")" "$5" "$4"
  }

  _caddy_site "$prefix" "$ip" console  3000   "$tls_block"  # console UI
  _caddy_site "$prefix" "$ip" api      9000   "$tls_block"  # agent-manager REST API
  _caddy_site "$prefix" "$ip" thunder  8080   "$tls_block"  # Thunder OAuth (OC kgateway, host-routed)
  _caddy_site "$prefix" "$ip" observer 9098   "$tls_block"  # traces observer
  _caddy_site "$prefix" "$ip" gateway  22893  "$tls_block"  # api-platform gateway: OTel + agent endpoints

  if [[ "$external_gateways" == "true" ]]; then
    # 9243 is HTTPS with a self-signed cert -> proxy over TLS, skip verification.
    # reverse_proxy upgrades the gateway control WebSocket transparently.
    printf '%s%s {\n%s\treverse_proxy 127.0.0.1:9243 {\n\t\ttransport http {\n\t\t\ttls\n\t\t\ttls_insecure_skip_verify\n\t\t}\n\t}\n}\n\n' \
      "$prefix" "$(vm_host cp "$ip")" "$tls_block"
  fi

  unset -f _caddy_site
}
