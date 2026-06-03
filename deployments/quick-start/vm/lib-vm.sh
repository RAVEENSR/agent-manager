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

  printf '%s\n' \
    "--set" "agentManager.config.serverPublicURL=${scheme}://${api}" \
    "--set" "agentManager.config.oauthAuthorizationServers=${scheme}://${thunder}" \
    "--set" "agentManager.config.keyManager.issuer=${scheme}://${thunder}" \
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

# render_caddyfile <ip> <scheme> <acme_email> <external_gateways:true|false (default true)>
# Prints a complete Caddyfile to stdout.
render_caddyfile() {
  local ip="$1" scheme="$2" email="$3" external_gateways="${4:-true}"
  local prefix=""              # https: bare host (Caddy auto-HTTPS)
  [[ "$scheme" == "http" ]] && prefix="http://"

  # Global options block.
  if [[ "$scheme" == "http" ]]; then
    printf '{\n\tauto_https off\n}\n\n'
  elif [[ -n "$email" ]]; then
    printf '{\n\temail %s\n}\n\n' "$email"
  fi
  # https + no email: intentionally no global block; Caddy uses its own default ACME contact.

  _caddy_site() {   # _caddy_site <prefix> <ip> <subdomain> <upstream_port>
    printf '%s%s {\n\treverse_proxy 127.0.0.1:%s\n}\n\n' "$1" "$(vm_host "$3" "$2")" "$4"
  }

  _caddy_site "$prefix" "$ip" console  3000   # console UI
  _caddy_site "$prefix" "$ip" api      9000   # agent-manager REST API
  _caddy_site "$prefix" "$ip" thunder  8080   # Thunder OAuth (OC kgateway, host-routed)
  _caddy_site "$prefix" "$ip" observer 9098   # traces observer
  _caddy_site "$prefix" "$ip" gateway  22893  # api-platform gateway: OTel + agent endpoints

  if [[ "$external_gateways" == "true" ]]; then
    # 9243 is HTTPS with a self-signed cert -> proxy over TLS, skip verification.
    # reverse_proxy upgrades the gateway control WebSocket transparently.
    printf '%s%s {\n\treverse_proxy 127.0.0.1:9243 {\n\t\ttransport http {\n\t\t\ttls\n\t\t\ttls_insecure_skip_verify\n\t\t}\n\t}\n}\n\n' \
      "$prefix" "$(vm_host cp "$ip")"
  fi

  unset -f _caddy_site
}
