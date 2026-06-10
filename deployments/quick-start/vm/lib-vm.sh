#!/usr/bin/env bash
# lib-vm.sh — pure helpers for the VM standalone install.
# Sourcing this file has no side effects; every function writes only to stdout.
#
# The VM installer is Let's Encrypt only and 443-only: certificates issue via the
# TLS-ALPN-01 ACME challenge (inside the :443 handshake), so no inbound port 80 is
# ever required and every public URL is https.

# vm_host <subdomain> <ip> -> "<sub>.amp.<ip>.sslip.io"
vm_host() {
  printf '%s.amp.%s.sslip.io' "$1" "$2"
}

# build_amp_helm_args <ip> <external_gateways:true|false (default true)>
# Prints helm args, one token per line (--set and KEY=VALUE on separate lines).
# Consume with (bash >=4):  mapfile -t ARR < <(build_amp_helm_args ...)
# bash 3.2 (macOS):         while IFS= read -r l; do ARR+=("$l"); done < <(build_amp_helm_args ...)
build_amp_helm_args() {
  local ip="$1" external_gateways="${2:-true}"
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
  #
  # config.tlsEnabled (env TLS_ENABLED) selects which advertised endpoint variant
  # amp-api hands the console for deployed agents: when true it emits the https URL
  # from the release binding instead of the http one. It does NOT change amp-api's
  # own serving (that is internalServer.tlsEnabled) — it is purely the endpoint
  # scheme. The agent host is only reachable over TLS via Caddy's wildcard site, so
  # without this the console emits http:// and the browser blocks it as mixed content.
  local k
  for k in agentManager agentManagerService; do
    printf '%s\n' \
      "--set" "${k}.config.serverPublicURL=https://${api}" \
      "--set" "${k}.config.oauthAuthorizationServers=https://${thunder}" \
      "--set" "${k}.config.keyManager.issuer=https://${thunder}" \
      "--set" "${k}.config.tlsEnabled=true"
  done

  printf '%s\n' \
    "--set" "console.config.auth.baseUrl=https://${thunder}" \
    "--set" "console.config.auth.signInRedirectURL=https://${console_h}/login" \
    "--set" "console.config.auth.signOutRedirectURL=https://${console_h}/login" \
    "--set" "console.config.apiBaseUrl=https://${api}" \
    "--set" "console.config.obsApiBaseUrl=https://${observer}" \
    "--set" "console.config.instrumentationUrl=https://${gateway}/otel"

  if [[ "$external_gateways" == "true" ]]; then
    # Full URL: the console parses it with new URL() to build gateway setup commands.
    printf '%s\n' "--set" "console.config.gatewayControlPlaneUrl=https://$(vm_host cp "$ip")"
  fi
}

# build_gateway_helm_args <ip>
# Prints GATEWAY_HELM_ARGS tokens. Sets the published vhost so deployed-agent
# endpoint URLs are externally reachable (path-routed under this single host),
# and points the gateway runtime's user-token key manager (ThunderKeyManager) at
# the public Thunder issuer. The runtime validates the JWT `iss` claim
# (validateissuer=true); user tokens are minted by the public Thunder, so without
# this invoking a deployed agent 401s.
#
# The whole keymanagers list is supplied via --set-json: a `--set keymanagers[1].issuer`
# does NOT merge into the chart's list, it replaces it with [null, {issuer}], which
# wipes keymanager[0] (agent-manager-service, used for OTel ingest) and the name/jwks
# of [1] -> malformed config.toml -> gateway crash loop. So both entries are restated
# in full; only the ThunderKeyManager issuer differs from the chart default. This is
# chart-version-coupled: re-verify both keymanagers (names + jwks URIs) on chart bumps.
build_gateway_helm_args() {
  local ip="$1" thunder keymanagers
  thunder="https://$(vm_host thunder "$ip")"
  keymanagers=$(printf '[{"name":"agent-manager-service","issuer":"agent-manager-service","jwks":{"remote":{"uri":"http://amp-api.wso2-amp.svc.cluster.local:9000/auth/external/jwks.json","skipTlsVerify":true}}},{"name":"ThunderKeyManager","issuer":"%s","jwks":{"remote":{"uri":"http://amp-thunder-extension-service.amp-thunder:8090/oauth2/jwks","skipTlsVerify":true}}}]' "$thunder")
  printf '%s\n' \
    "--set" "gateway.vhost=https://$(vm_host gateway "$ip")" \
    "--set-json" "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers=${keymanagers}"
}

# build_observability_helm_args <ip>
# Prints OBSERVABILITY_HELM_ARGS tokens. The traces observer validates the same
# user token (its `iss` must match), so the console's traces page 401s until its
# issuer is the public Thunder URL too. jwksUrl stays on the in-cluster service.
build_observability_helm_args() {
  local ip="$1"
  printf '%s\n' \
    "--set" "tracesObserver.auth.issuer=https://$(vm_host thunder "$ip")"
}

# build_cp_helm_args <ip>
# Prints CP_HELM_ARGS tokens for the OpenChoreo control-plane install. Thunder's
# issuer is moved to the public sslip.io URL, so the OpenChoreo CP OIDC config
# (which validates the issuer string statically) must accept that same issuer —
# otherwise amp-api -> OpenChoreo calls fail with "INVALID_CLAIMS". jwksUrl /
# wellKnownEndpoint stay on the internal service (they still resolve in-cluster).
build_cp_helm_args() {
  local ip="$1" thunder
  thunder="$(vm_host thunder "$ip")"
  printf '%s\n' \
    "--set" "security.oidc.issuer=https://${thunder}" \
    "--set" "security.oidc.authorizationUrl=https://${thunder}/oauth2/authorize" \
    "--set" "security.oidc.tokenUrl=https://${thunder}/oauth2/token"
}

# build_platform_resources_helm_args
# Prints PLATFORM_RESOURCES_HELM_ARGS tokens. The platform-resources chart's
# workload-publisher defaults its OAuth token endpoint to the kgateway path
# (`host.k3d.internal:8080/oauth2/token` + Host `thunder.amp.localhost`). On the
# VM that route no longer matches: build_cp_helm_args / build_thunder_helm_args
# move Thunder's vhost to the public sslip.io host, so the localhost Host header
# 404s and `generate-workload-cr` fails with "Failed to get access token". Point
# it at the Thunder service directly (no gateway, no Host header, no issuer
# coupling) — the same in-cluster endpoint every other extension already uses.
build_platform_resources_helm_args() {
  printf '%s\n' \
    "--set" "global.oauth.tokenUrl=http://amp-thunder-extension-service.amp-thunder.svc.cluster.local:8090/oauth2/token"
}

# build_thunder_helm_args <ip>
# Prints helm args, one token per line.
build_thunder_helm_args() {
  local ip="$1" thunder console_h
  thunder="$(vm_host thunder "$ip")"
  console_h="$(vm_host console "$ip")"

  printf '%s\n' \
    "--set" "thunder.ocIngress.hostname=${thunder}" \
    "--set" "thunder.configuration.server.publicUrl=https://${thunder}" \
    "--set" "thunder.configuration.jwt.issuer=https://${thunder}" \
    "--set" "thunder.configuration.gateClient.hostname=${thunder}" \
    "--set" "thunder.configuration.gateClient.scheme=https" \
    "--set" "thunder.configuration.gateClient.port=443" \
    "--set" "thunder.configuration.cors.allowedOrigins[0]=https://${console_h}"

  # The console client's registered redirect URI lives under `setup` (<=main) and
  # was renamed to `bootstrap` (>=0.15.0, which is what the registration template
  # actually reads). Emit both; helm ignores the inert one. Must match the
  # console's signInRedirectURL or Thunder rejects login with "Invalid redirect URI".
  local k
  for k in setup bootstrap; do
    printf '%s\n' "--set" "thunder.${k}.ampConsoleClient.redirectUris[0]=https://${console_h}/login"
  done
}

# render_k3d_vm_config [node_host]  (reads k3d config on stdin, writes VM config on stdout)
# Two rewrites:
#  1. '- port: <host>:<container>' -> '- port: 127.0.0.1:<host>:<container>' so the
#     k3d host ports bind to loopback only. Already-bound entries are left untouched.
#  2. The containerd registry mirror *endpoint* host.k3d.internal:10082 -> <node_host>:10082.
#     The mirror key stays host.k3d.internal:10082 (it must match the image tag the
#     publish step writes), but the node's containerd resolves host.k3d.internal via
#     its own /etc/hosts to the Docker bridge gateway — which has nothing listening
#     once ports are loopback-bound, so agent image pulls fail with ImagePullBackOff.
#     The node *can* reach the registry LoadBalancer at its own node hostname, which
#     k3d puts in the node's /etc/hosts (IP-independent). Pod-side DNS is handled
#     separately by render_coredns_vm_config; this covers the node containerd path.
render_k3d_vm_config() {
  local node_host="${1:-k3d-amp-local-server-0}"
  sed -E \
    -e 's/^([[:space:]]*- port: )([0-9]+:[0-9]+)/\1127.0.0.1:\2/' \
    -e "s#^([[:space:]]*- )http://host\\.k3d\\.internal:10082#\\1http://${node_host}:10082#"
}

# render_coredns_vm_config <node_host>
# Prints a `coredns-custom` ConfigMap that rewrites the in-cluster *.localhost /
# host.k3d.internal names to the k3d server node (<node_host>, e.g.
# k3d-amp-local-server-0), instead of the base config's `host.k3d.internal`.
#
# Why the VM needs this: the stock config points these names at host.k3d.internal,
# which ensure_coredns_host_aliases maps to the Docker bridge gateway (the host),
# relying on a host hairpin to the published service ports. But the VM installer
# binds every k3d host port to 127.0.0.1 (render_k3d_vm_config), so the gateway IP
# has nothing listening — observer->authz (build logs) and the registry push/pull
# both fail with "connection refused". The server node is where klipper exposes
# all the LoadBalancer service ports, so rewriting straight to its hostname is
# reachable and, unlike a NodeHosts alias, survives k3s NodeHosts reconciliation
# (the node entry is always present). Applied via install.sh's COREDNS_FILE hook.
render_coredns_vm_config() {
  local node_host="$1"
  cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-custom
  namespace: kube-system
data:
  amp.override: |
    rewrite stop {
      name regex (.+\\.)?amp\\.localhost ${node_host}
      answer auto
    }
  openchoreo.override: |
    rewrite stop {
      name regex (.+\\.)?openchoreo\\.localhost ${node_host}
      answer auto
    }
  hostalias.override: |
    rewrite stop {
      name regex (host\\.k3d\\.internal|host\\.docker\\.internal) ${node_host}
      answer auto
    }
EOF
}

# render_dataplane_external_ingress <ip>
# Prints the `external:` http/https entries for install.sh's ClusterDataPlane
# (DP_EXTERNAL_INGRESS hook), advertising deployed-agent endpoints under the
# public host <org>-<project>.agents.<ip>.sslip.io instead of the local default
# openchoreoapis.localhost:19080.
#
# Emits BOTH entries on :443, bound to the internal http listener (TLS is
# terminated at Caddy's wildcard *.agents site). Both variants resolve to the same
# host:port/path and differ only in scheme; amp-api advertises the https one to the
# console (build_amp_helm_args sets config.tlsEnabled=true), so the browser calls
# https://...:443 directly and the wildcard site serves it. The http entry is kept
# too: a release binding missing a variant makes the console fall back to a relative
# /chat (405 from its own nginx), so emitting both keeps the binding complete.
render_dataplane_external_ingress() {
  local ip="$1"
  local host="agents.${ip}.sslip.io"
  printf '        http:\n          host: "%s"\n          listenerName: http\n          port: 443\n' "$host"
  printf '        https:\n          host: "%s"\n          listenerName: http\n          port: 443\n' "$host"
}

# render_caddyfile <ip> <acme_email> <external_gateways:true|false (default true)>
# Prints a complete Caddyfile to stdout. Let's Encrypt only, 443-only: every site
# forces the TLS-ALPN-01 challenge so issuance never needs inbound port 80, and the
# :80 http->https redirect is dropped (port 80 is never opened).
render_caddyfile() {
  local ip="$1" email="$2" external_gateways="${3:-true}"
  local console_origin
  console_origin="https://$(vm_host console "$ip")"

  # Every public site forces the TLS-ALPN-01 ACME challenge (it runs inside the
  # :443 TLS handshake) so certificate issuance never depends on inbound port 80.
  local tls_block=$'\ttls {\n\t\tissuer acme {\n\t\t\tdisable_http_challenge\n\t\t}\n\t}\n'

  # Global options: optional ACME contact email, drop the :80 http->https redirect
  # (port 80 is never opened), and enable on-demand TLS for the per-agent wildcard
  # hosts. The ask endpoint (loopback site below) approves any host; Caddy only
  # triggers on-demand for SNI matching the *.agents wildcard, so issuance is bounded
  # to that namespace (a route-validating ask is a follow-up).
  local gopts=""
  [[ -n "$email" ]] && gopts+=$'\temail '"$email"$'\n'
  gopts+=$'\tauto_https disable_redirects\n'
  gopts+=$'\ton_demand_tls {\n\t\task http://127.0.0.1:9753\n\t}\n'
  printf '{\n%s}\n\n' "$gopts"

  _caddy_site() {   # _caddy_site <ip> <subdomain> <upstream_port> <tls_block> [extra]
    printf '%s {\n%s%s\treverse_proxy 127.0.0.1:%s\n}\n\n' "$(vm_host "$2" "$1")" "$4" "${5:-}" "$3"
  }

  _caddy_site "$ip" console  3000   "$tls_block"  # console UI
  _caddy_site "$ip" api      9000   "$tls_block"  # agent-manager REST API
  _caddy_site "$ip" thunder  8080   "$tls_block"  # Thunder OAuth (OC kgateway, host-routed)
  _caddy_site "$ip" observer 9098   "$tls_block"  # traces observer
  _caddy_site "$ip" gateway  22893  "$tls_block"  # api-platform gateway: OTel ingest

  if [[ "$external_gateways" == "true" ]]; then
    # 9243 is HTTPS with a self-signed cert -> proxy over TLS, skip verification.
    # reverse_proxy upgrades the gateway control WebSocket transparently.
    printf '%s {\n%s\treverse_proxy 127.0.0.1:9243 {\n\t\ttransport http {\n\t\t\ttls\n\t\t\ttls_insecure_skip_verify\n\t\t}\n\t}\n}\n\n' \
      "$(vm_host cp "$ip")" "$tls_block"
  fi

  # Deployed-agent endpoints: <org>-<project>.agents.<ip>.sslip.io (one host per
  # org/project, dynamic), proxied to the data-plane gateway with on-demand TLS
  # (per-host certs at first hit) + CORS (the gateway adds none); X-API-Key is the
  # header the console sends the token in.
  printf 'http://127.0.0.1:9753 {\n\trespond 200\n}\n\n'   # on-demand TLS ask (always-allow; see note above)
  local agent_tls=$'\ttls {\n\t\ton_demand\n\t\tissuer acme {\n\t\t\tdisable_http_challenge\n\t\t}\n\t}\n'
  local cors_block
  cors_block=$(printf '\theader {\n\t\tAccess-Control-Allow-Origin "%s"\n\t\tAccess-Control-Allow-Methods "GET, POST, PUT, DELETE, PATCH, OPTIONS"\n\t\tAccess-Control-Allow-Headers "Authorization, Content-Type, X-API-Key"\n\t\tAccess-Control-Allow-Credentials "true"\n\t\tAccess-Control-Max-Age "3600"\n\t\tVary Origin\n\t\tdefer\n\t}\n\t@cors_preflight method OPTIONS\n\trespond @cors_preflight 204\n' "$console_origin")
  printf '*.agents.%s.sslip.io {\n%s%s\n\treverse_proxy 127.0.0.1:19080\n}\n\n' "$ip" "$agent_tls" "$cors_block"

  unset -f _caddy_site
}
