#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
set -euo pipefail

# Provisions a dedicated Thunder ID instance for ONE environment (the home of that
# environment's agent identities). The platform Thunder (amp-thunder, console login)
# is separate and untouched — env-Thunders are added alongside it.
#
# Unlike platform Thunder (which is installed via the agent-manager-owned
# wso2-amp-thunder-extension chart, including its full console/API/roles/groups
# bootstrap), env-Thunder installs the upstream ThunderID release chart DIRECTLY
# (oci://ghcr.io/thunder-id/helm-charts/thunderid — see
# https://thunderid.dev/docs/next/guides/getting-started/get-thunderid/). This
# decouples env-Thunder's version from whatever version platform Thunder happens to
# run (including platform Thunder being rolled back), and from the agent-manager
# release cadence — no wso2-amp-thunder-extension release is required to pick up a
# new ThunderID version here. Everything env-Thunder needs beyond the bare chart
# (trusted-issuer wiring, the amp-system-client OAuth app, external routing) is
# applied by this script directly, using the upstream chart's own native knobs
# (configuration.server.security.trustedIssuer, bootstrap.configMap, setup.admin,
# declarativeResources) or plain kubectl-applied manifests.
#
# All inputs are provided via environment variables so the script can be piped
# directly into bash:
#
#   curl -fsSL https://raw.githubusercontent.com/wso2/agent-manager/main/deployments/scripts/add-environment-thunder.sh \
#     | ENV_NAME=staging \
#       DISPLAY_NAME="Staging" \
#       bash
#
# Re-running with the same ENV_NAME is idempotent (helm upgrade --install; the
# system-client secret is reused, never rotated).
#
# Prerequisites:
#   - kubectl and helm must be configured
#   - ENV_NAME: resource name (lowercase alphanumeric with hyphens)
#   - DISPLAY_NAME: human-readable name
# Optional:
#   - ORG_NAME (default: default)
#   - THUNDER_CHART: override the chart ref (default: oci://ghcr.io/thunder-id/helm-charts/thunderid —
#     the upstream ThunderID release chart, pulled directly, NOT the agent-manager chart)
#   - CHART_VERSION: pin the chart version (default: 0.45.0; OCI charts only)
#   - SYSTEM_CLIENT_SECRET (default: generated; reused if one already exists)
#   - THUNDER_ADMIN_PASSWORD (default: generated 10-char password w/ letters, digits,
#     and symbols; reused if one already exists) — native ThunderID superadmin password
#     for THIS env-Thunder's own /console. Printed at the end of this script's output;
#     not saved to disk. Stored server-side as a K8s Secret (<release>-admin-credentials,
#     key "password") so re-running the script reuses it instead of rotating it.
#   - PERSISTENCE_SIZE (default: 1Gi), STORAGE_CLASS (default: cluster default)
#   - WAIT_TIMEOUT (default: 180s)
#   Delivering the system-client secret to agent-manager-service (AMS): the secret
#   is handed to AMS over HTTP, which encrypts it and stores it in its own database
#   (AMS decrypts it in-process when needed — it is never read back from a key
#   vault). AMS must be running and reachable when this step runs.
#   - AMP_API_URL (default: http://localhost:9000/api/v1) — AMS API base URL.
#     Local dev (docker-compose): http://localhost:9000/api/v1. k3d/quick-start:
#     http://api.amp.localhost:8080/api/v1. Set explicitly for other topologies.
#   - AGENT_MANAGER_TOKEN (default: unset) — a Bearer token with the
#     org:manage-service-account permission. If unset, this script obtains one via
#     a client_credentials grant using IDP_* below.
#   - IDP_TOKEN_URL (default: http://thunder.amp.localhost:8080/oauth2/token) —
#     platform Thunder token endpoint (used only when AGENT_MANAGER_TOKEN is unset).
#   - IDP_CLIENT_ID (default: amp-api-client), IDP_CLIENT_SECRET
#     (default: amp-api-client-secret) — the platform system client to grant as.
#     AMS derives the stored credential's OU ID from this token itself (never
#     from a client-supplied value — see models.EnvThunderSystemClient's doc
#     comment; org_name is not persisted for this credential at all).
#   Platform Thunder trusted-issuer (env-Thunder accepts platform Thunder tokens):
#   - PLATFORM_THUNDER_ISSUER   (default: http://thunder.amp.localhost:8080)
#   - PLATFORM_THUNDER_JWKS_URL (default: HTTPS JWKS endpoint of platform Thunder)
#   - PLATFORM_THUNDER_TOKEN_AUDIENCE (default: amp — the aud claim platform Thunder's
#     tokens carry once any amp:* scope is requested, since ThunderID composes aud from
#     the resource server(s) resolved via the granted scopes. A scopeless
#     client_credentials token instead carries the calling client's own ID as aud.)
#   Non-local-dev deployments (e.g. a VM — see deployments/vm/lib-vm.sh, which sets
#   all three of these together, deployment-wide, whenever it provisions env-Thunder):
#   - THUNDER_HOST_BASE_DOMAIN (default: amp.localhost) — the domain suffix env-Thunder's
#     hostnames are built from ("<org>-<env>.thunder.<this>"). MUST be set to the
#     identical value in agent-manager-service's own config (same env var name) on
#     any given deployment — see clients/thundersvc/naming.go's ThunderHost, which
#     independently computes the same value and has no way to learn about a
#     one-off override here.
#   - TLS_ENABLED (default: false) — when true, the issuer/publicUrl become
#     https://<host> with no explicit port (a VM's Caddy terminates TLS on the
#     standard HTTPS port) instead of http://<host>:8080 (the k3d gateway's
#     plain-HTTP port used in local dev). Same flag agent-manager-service and the
#     VM's own platform-Thunder Helm args already use for this exact purpose.
#   - SKIP_CA_BUNDLE_TRUST (default: false) — skip fetching/mounting a custom CA
#     bundle for the platform-Thunder trusted-issuer JWKS fetch. Set this when
#     platform Thunder's issuer is already backed by a real, publicly-trusted CA
#     (e.g. Let's Encrypt via a VM's Caddy) — the container's default trust store
#     already covers it, so there's nothing custom to mount. Leave false for local
#     dev, where platform Thunder's HTTPS gateway uses a self-signed CA that
#     nothing trusts by default.

# ---------------------------------------------------------------------------
# Pure helpers (sourced by the test suite; keep free of side effects).
# ---------------------------------------------------------------------------

# validate_name NAME -> 0 if a valid DNS-1123-ish label, non-zero otherwise.
validate_name() {
  printf '%s' "${1:-}" | grep -Eq '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'
}

# _sha256 FILE -> full SHA-256 hex of a file (portable: shasum or sha256sum).
_sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

# Load the shared Thunder naming helpers (_sha6/thunder_release_name/
# thunder_namespace/thunder_host/thunder_issuer) — the single source of truth
# for this derivation, see deployments/scripts/thunder-naming.sh. Prefers a
# local sibling file (checked-out repo, or the test suite sourcing this
# script); falls back to fetching it from the same ref this script itself
# would be fetched from when piped via curl | bash.
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "$(dirname "${BASH_SOURCE[0]}")/thunder-naming.sh" ]; then
  # shellcheck source=thunder-naming.sh
  source "$(dirname "${BASH_SOURCE[0]}")/thunder-naming.sh"
else
  _naming_lib_url="${THUNDER_NAMING_LIB_URL:-${SCRIPT_BASE_URL:-https://raw.githubusercontent.com/wso2/agent-manager/main/deployments/scripts}/thunder-naming.sh}"
  _naming_lib_tmp="$(mktemp)"
  if ! curl -fsSL "${_naming_lib_url}" -o "${_naming_lib_tmp}"; then
    echo "❌ Failed to fetch Thunder naming helpers from ${_naming_lib_url}" >&2
    rm -f "${_naming_lib_tmp}"
    exit 1
  fi
  # shellcheck source=/dev/null
  source "${_naming_lib_tmp}"
  rm -f "${_naming_lib_tmp}"
  unset _naming_lib_url _naming_lib_tmp
fi

# platform_thunder_issuer -> OIDC issuer of the shared platform Thunder instance.
# Env-Thunder trusts tokens bearing this issuer so callers can authenticate with
# a platform Thunder token instead of env-Thunder system-client credentials.
platform_thunder_issuer() {
  printf 'http://thunder.amp.localhost:8080'
}

# platform_thunder_jwks_url -> HTTPS JWKS URL that env-Thunder pods use to verify
# incoming platform Thunder tokens. Routed via the dedicated HTTPS Gateway on port
# 8443 (cert-manager-issued TLS, CA trusted via SSL_CERT_FILE inside the pod).
platform_thunder_jwks_url() {
  printf 'https://thunder.amp.localhost:8443/oauth2/jwks'
}

# platform_thunder_ca_cert -> prints the PEM CA cert that signed the
# thunder.amp.localhost TLS certificate, or returns 1 if not yet provisioned.
# Set PLATFORM_THUNDER_CA_PEM to inject a cert directly (useful in tests/CI).
platform_thunder_ca_cert() {
  if [ -n "${PLATFORM_THUNDER_CA_PEM:-}" ]; then
    printf '%s' "$PLATFORM_THUNDER_CA_PEM"
    return 0
  fi

  # Wait for platform Thunder's TLS cert to be ready so we avoid racing with cert-manager.
  # Redirect output to stderr (>&2) to prevent polluting the stdout captured by the caller.
  if kubectl get certificate amp-thunder-extension-local-tls -n openchoreo-control-plane >/dev/null 2>&1; then
    echo "⏳ Waiting for platform Thunder TLS certificate to be issued by cert-manager..." >&2
    kubectl wait --for=condition=Ready certificate/amp-thunder-extension-local-tls \
      -n openchoreo-control-plane --timeout=300s >&2 || {
        echo "⚠️  Platform Thunder TLS certificate not yet ready — trusted issuer may not be configured." >&2
      }
  fi

  local b64
  b64="$(kubectl get secret amp-local-root-ca-secret -n cert-manager \
    -o jsonpath='{.data.ca\.crt}' 2>/dev/null || true)"
  [ -z "$b64" ] && return 1
  printf '%s' "$b64" | _b64decode
}

# _b64decode (stdin) -> decoded bytes (openssl is portable across macOS/Linux).
_b64decode() {
  openssl base64 -d -A
}

# generate_admin_password -> a 10-character random password with letters, digits,
# and special characters (avoids ambiguous chars like 0/O/1/l/I). Bash builtins only
# (no external tools) for portability across macOS/Linux.
generate_admin_password() {
  local alnum='ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  local specials='!@%_='
  local chars=() i j tmp
  for ((i = 0; i < 8; i++)); do
    chars+=("${alnum:RANDOM % ${#alnum}:1}")
  done
  for ((i = 0; i < 2; i++)); do
    chars+=("${specials:RANDOM % ${#specials}:1}")
  done
  # Fisher-Yates shuffle so the two special characters aren't always at fixed positions.
  for ((i = ${#chars[@]} - 1; i > 0; i--)); do
    j=$((RANDOM % (i + 1)))
    tmp="${chars[i]}"; chars[i]="${chars[j]}"; chars[j]="$tmp"
  done
  printf '%s' "${chars[@]}"
}

# read_existing_secret NS NAME [KEY] -> prints the stored secret value (key
# defaults to "client-secret"), or returns 1 if the secret/key doesn't exist.
read_existing_secret() {
  local ns="$1" name="$2" key="${3:-client-secret}" b64
  b64="$(kubectl get secret "$name" -n "$ns" -o jsonpath="{.data.${key}}" 2>/dev/null || true)"
  [ -z "$b64" ] && return 1
  printf '%s' "$b64" | _b64decode
}

# Load the shared AMS auth helpers (json_escape/get_ams_token) — see
# deployments/scripts/ams-auth.sh. Same prefer-local-sibling,
# fallback-to-curl-fetch pattern as the thunder-naming.sh load above.
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "$(dirname "${BASH_SOURCE[0]}")/ams-auth.sh" ]; then
  # shellcheck source=ams-auth.sh
  source "$(dirname "${BASH_SOURCE[0]}")/ams-auth.sh"
else
  _ams_auth_lib_url="${AMS_AUTH_LIB_URL:-${SCRIPT_BASE_URL:-https://raw.githubusercontent.com/wso2/agent-manager/main/deployments/scripts}/ams-auth.sh}"
  _ams_auth_lib_tmp="$(mktemp)"
  if ! curl -fsSL "${_ams_auth_lib_url}" -o "${_ams_auth_lib_tmp}"; then
    echo "❌ Failed to fetch AMS auth helpers from ${_ams_auth_lib_url}" >&2
    rm -f "${_ams_auth_lib_tmp}"
    exit 1
  fi
  # shellcheck source=/dev/null
  source "${_ams_auth_lib_tmp}"
  rm -f "${_ams_auth_lib_tmp}"
  unset _ams_auth_lib_url _ams_auth_lib_tmp
fi

# store_via_ams ORG ENV CLIENT_ID SECRET — hands the credential to AMS over
# HTTP; AMS encrypts and stores it (never read back from a vault). Idempotent (PUT).
store_via_ams() {
  local org="$1" env_name="$2" client_id="$3" secret_val="$4"
  local amp_api_url="${AMP_API_URL:-http://localhost:9000/api/v1}"

  local access_token
  if ! access_token="$(get_ams_token)"; then
    echo "⚠️  Could not obtain an access token to call agent-manager-service."
    echo "   Set AGENT_MANAGER_TOKEN, or check IDP_TOKEN_URL/IDP_CLIENT_ID/IDP_CLIENT_SECRET"
    echo "   and that platform Thunder is reachable."
    return 1
  fi

  # AMS derives the credential's OU ID from this token itself (never from a
  # client-supplied value, to prevent one org's token from writing another
  # org's credential) — nothing to resolve or send here beyond the token.
  # json_escape guards against a custom SYSTEM_CLIENT_SECRET breaking the JSON body.
  local body
  body="$(printf '{"clientId":"%s","clientSecret":"%s"}' "$(json_escape "$client_id")" "$(json_escape "$secret_val")")"

  local http_code
  http_code="$(curl -s -o /dev/null -w "%{http_code}" \
    --max-time 30 --retry 5 --retry-delay 5 \
    -X PUT "${amp_api_url}/orgs/${org}/environments/${env_name}/thunder-system-client" \
    -H "Authorization: Bearer ${access_token}" \
    -H "Content-Type: application/json" \
    -d "${body}" 2>/dev/null)"
  # curl's own -w already writes "000" when no response is received; falling
  # back with `|| echo "000"` on top of that double-appends into "000000".
  http_code="${http_code:-000}"

  case "$http_code" in
    200|204)
      echo "🔐 Stored system-client secret in agent-manager-service (org=${org}, env=${env_name})"
      return 0
      ;;
  esac

  echo "⚠️  Could not store the system-client secret in agent-manager-service (HTTP ${http_code})."
  echo "   Check that AMP_API_URL (${amp_api_url}) is reachable and the token has the"
  echo "   org:manage-service-account permission, then re-run add-environment-thunder.sh."
  return 1
}

# render_system_client_bootstrap_script SECRET -> prints a plain (non-Helm-templated)
# bootstrap script that registers the amp-system-client OAuth2 app and assigns it to
# ThunderID's own native "Administrator" role (created automatically by every
# ThunderID install — no AMP-specific role/resource-server bootstrap needed).
#
# This is the ONLY bootstrap env-Thunder needs: agent-manager-service uses this one
# client_credentials app (see agent-manager-service/clients/thundersvc/naming.go) to
# call env-Thunder's admin API and create per-agent OAuth2 apps at runtime. The
# console/CLI/MCP/workload-publisher/observer-reader clients and the AMP-specific
# roles/groups bootstrapped for platform Thunder are human-console concerns that
# env-Thunder (agent identities only) does not need.
render_system_client_bootstrap_script() {
  local secret="$1" script
  script="$(cat <<'BOOTSTRAP_SCRIPT'
#!/bin/bash
set -e

SCRIPT_DIR="$(dirname "${BASH_SOURCE[0]:-$0}")"
source "${SCRIPT_DIR}/common.sh"

CLIENT_ID="amp-system-client"
CLIENT_NAME="AMP System Client"
CLIENT_DESC="System client for agent-manager to provision per-org OAuth apps"
CLIENT_SECRET="__SYSTEM_CLIENT_SECRET__"

log_info "Checking if application '${CLIENT_NAME}' already exists..."

RESPONSE=$(api_call GET "/organization-units/tree/default")
HTTP_CODE="${RESPONSE: -3}"
BODY="${RESPONSE%???}"
if [[ "$HTTP_CODE" != "200" ]]; then
  log_error "Failed to fetch default organization unit (HTTP $HTTP_CODE)"
  echo "Response: $BODY"
  exit 1
fi
DEFAULT_OU_ID=$(echo "$BODY" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
if [[ -z "$DEFAULT_OU_ID" ]]; then
  log_error "Could not extract default organization unit ID from response"
  exit 1
fi

RESPONSE=$(api_call GET "/flows?flowType=AUTHENTICATION&limit=200")
HTTP_CODE="${RESPONSE: -3}"
BODY="${RESPONSE%???}"
if [[ "$HTTP_CODE" != "200" ]]; then
  log_error "Failed to fetch authentication flows (HTTP $HTTP_CODE)"
  exit 1
fi
AUTH_FLOW_ID=$(echo "$BODY" | sed 's/},{/}\n{/g' | grep '"handle":"default-basic-flow"' | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
if [[ -z "$AUTH_FLOW_ID" ]]; then
  log_error "Could not find default-basic-flow authentication flow"
  exit 1
fi

RESPONSE=$(api_call GET "/applications")
HTTP_CODE="${RESPONSE: -3}"
BODY="${RESPONSE%???}"
if [[ "$HTTP_CODE" != "200" ]]; then
  log_error "Failed to fetch applications (HTTP $HTTP_CODE)"
  exit 1
fi

APP_PAYLOAD='{
  "name": "'"${CLIENT_NAME}"'",
  "description": "'"${CLIENT_DESC}"'",
  "ouId": "'${DEFAULT_OU_ID}'",
  "authFlowId": "'${AUTH_FLOW_ID}'",
  "inboundAuthConfig": [
    {
      "type": "oauth2",
      "config": {
        "clientId": "'"${CLIENT_ID}"'",
        "clientSecret": "'"${CLIENT_SECRET}"'",
        "grantTypes": ["client_credentials"],
        "tokenEndpointAuthMethod": "client_secret_basic",
        "pkceRequired": false,
        "publicClient": false,
        "token": {
          "accessToken": {
            "validityPeriod": 3600
          }
        }
      }
    }
  ]
}'

SYSTEM_APP_ID=""
if echo "$BODY" | grep -q "\"clientId\":\"${CLIENT_ID}\""; then
  SYSTEM_APP_ID=$(echo "$BODY" | sed 's/},{/}\n{/g' | grep "\"clientId\":\"${CLIENT_ID}\"" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
  log_info "Application '${CLIENT_NAME}' already exists (id: $SYSTEM_APP_ID), updating..."
  RESPONSE=$(api_call PUT "/applications/$SYSTEM_APP_ID" "$APP_PAYLOAD")
  HTTP_CODE="${RESPONSE: -3}"
  if [[ "$HTTP_CODE" != "200" ]]; then
    log_error "Failed to update application (HTTP $HTTP_CODE)"
    exit 1
  fi
else
  log_info "Application '${CLIENT_NAME}' does not exist, creating..."
  RESPONSE=$(api_call POST "/applications" "$APP_PAYLOAD")
  HTTP_CODE="${RESPONSE: -3}"
  BODY="${RESPONSE%???}"
  if [[ "$HTTP_CODE" == "201" ]] || [[ "$HTTP_CODE" == "200" ]]; then
    SYSTEM_APP_ID=$(echo "$BODY" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
    log_info "Application '${CLIENT_NAME}' created successfully (id: $SYSTEM_APP_ID)"
  elif [[ "$HTTP_CODE" == "409" ]]; then
    RESPONSE=$(api_call GET "/applications")
    BODY="${RESPONSE%???}"
    SYSTEM_APP_ID=$(echo "$BODY" | sed 's/},{/}\n{/g' | grep "\"clientId\":\"${CLIENT_ID}\"" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
  else
    log_error "Failed to create application (HTTP $HTTP_CODE)"
    echo "Response: $BODY"
    exit 1
  fi
fi

if [[ -z "$SYSTEM_APP_ID" ]]; then
  log_error "Could not determine system app ID"
  exit 1
fi

log_info "Looking up native Administrator role..."
RESPONSE=$(api_call GET "/roles")
HTTP_CODE="${RESPONSE: -3}"
BODY="${RESPONSE%???}"
if [[ "$HTTP_CODE" != "200" ]]; then
  log_error "Failed to fetch roles (HTTP $HTTP_CODE)"
  exit 1
fi
ADMIN_ROLE_ID=$(echo "$BODY" | sed 's/},{/}\n{/g' | grep '"name":"Administrator"' | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
if [[ -z "$ADMIN_ROLE_ID" ]]; then
  log_error "Could not find native Administrator role"
  exit 1
fi

log_info "Assigning system app to Administrator role..."
ASSIGN_PAYLOAD='{"assignments":[{"id":"'${SYSTEM_APP_ID}'","type":"app"}]}'
RESPONSE=$(api_call POST "/roles/$ADMIN_ROLE_ID/assignments/add" "$ASSIGN_PAYLOAD")
HTTP_CODE="${RESPONSE: -3}"
if [[ "$HTTP_CODE" == "200" ]] || [[ "$HTTP_CODE" == "201" ]] || [[ "$HTTP_CODE" == "204" ]] || [[ "$HTTP_CODE" == "409" ]]; then
  log_success "System app assigned to Administrator role"
else
  log_error "Failed to assign system app to Administrator role (HTTP $HTTP_CODE)"
  exit 1
fi
BOOTSTRAP_SCRIPT
)"
  printf '%s' "${script//__SYSTEM_CLIENT_SECRET__/$secret}"
}

# apply_httproute RELEASE NAMESPACE HOST PORT — routes ${HOST}:8080 to the env-Thunder
# Service via the shared `gateway-default` Gateway in openchoreo-control-plane.
# gateway-default only allows HTTPRoutes from its own namespace, so the route (and a
# ReferenceGrant authorizing it to reach a Service in another namespace) are created
# there directly. Kept as plain manifests (not a Helm chart) since the upstream
# thunderid chart's own httproute/gateway support assumes a same-namespace Gateway,
# and this is the same routing platform Thunder relies on.
apply_httproute() {
  local release="$1" ns="$2" host="$3" port="$4"
  cat <<EOF | kubectl apply -f - >/dev/null
---
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: amp-thunder-backend
  namespace: ${ns}
  labels:
    app.kubernetes.io/instance: ${release}
    app.kubernetes.io/managed-by: add-environment-thunder.sh
    app.kubernetes.io/name: thunderid
spec:
  from:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      namespace: openchoreo-control-plane
  to:
    - group: ""
      kind: Service
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: ${release}
  namespace: openchoreo-control-plane
  labels:
    app.kubernetes.io/instance: ${release}
    app.kubernetes.io/managed-by: add-environment-thunder.sh
    app.kubernetes.io/name: thunderid
spec:
  hostnames:
    - ${host}
  parentRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: gateway-default
  rules:
    - backendRefs:
        - group: ""
          kind: Service
          name: ${release}-service
          namespace: ${ns}
          port: ${port}
          weight: 1
      matches:
        - path:
            type: PathPrefix
            value: /
EOF
}

# patch_ca_bundle_mount RELEASE NAMESPACE CA_CONFIGMAP -> mounts the platform-CA
# ConfigMap into the env-Thunder Deployment via a strategic-merge kubectl patch,
# and sets SSL_CERT_FILE to point at it.
#
# NOT done via the chart's declarativeResources support: enabling it doesn't just
# mount extra files — it flips a GLOBAL server-side "declarative mode" flag that
# makes i18n translations read-only. ThunderID's OWN setup Job always tries to
# POST-seed default i18n translations regardless, so it fails with HTTP 400
# DCR-1002 "declarative_resource.update_operation_not_allowed" and the whole
# pre-install hook fails. The setup Job never needs this CA bundle anyway (its
# bootstrap scripts only call the LOCAL server on localhost:8090, never platform
# Thunder), so patching the Deployment after install — instead of setting a chart
# value before install — avoids the global flag entirely. Idempotent:
# `containers`/`volumes`/`env` merge by their name/mountPath key, so re-applying
# the same patch on every re-run is a no-op.
patch_ca_bundle_mount() {
  local release="$1" ns="$2" ca_cm_name="$3"
  kubectl patch deployment "${release}-deployment" -n "$ns" --type=strategic -p "$(cat <<EOF
{
  "spec": {
    "template": {
      "spec": {
        "volumes": [
          {"name": "platform-ca", "configMap": {"name": "${ca_cm_name}"}}
        ],
        "containers": [
          {
            "name": "thunderid",
            "volumeMounts": [
              {"name": "platform-ca", "mountPath": "/etc/ssl/amp/ca-bundle.crt", "subPath": "ca-bundle.crt", "readOnly": true}
            ],
            "env": [
              {"name": "SSL_CERT_FILE", "value": "/etc/ssl/amp/ca-bundle.crt"}
            ]
          }
        ]
      }
    }
  }
}
EOF
)" >/dev/null
}

# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------
main() {
  : "${ENV_NAME:?ENV_NAME is required (e.g. ENV_NAME=staging)}"
  : "${DISPLAY_NAME:?DISPLAY_NAME is required (e.g. DISPLAY_NAME=\"Staging\")}"

  local org="${ORG_NAME:-default}"

  if ! validate_name "$ENV_NAME"; then
    echo "❌ Invalid ENV_NAME '${ENV_NAME}'"
    echo "   Must be lowercase alphanumeric with hyphens (no leading/trailing hyphen)."
    exit 1
  fi
  if ! validate_name "$org"; then
    echo "❌ Invalid ORG_NAME '${org}'"
    echo "   Must be lowercase alphanumeric with hyphens (no leading/trailing hyphen)."
    exit 1
  fi

  # Namespace/host are ALWAYS computed from (org, env) — never overridable. Every
  # other consumer of this env-Thunder (the gateway's ThunderKeyManager wiring in
  # add-environment.sh, and agent-manager-service's naming.go, which the future
  # EnvThunderResolver resolves per-agent OAuth clients against) recomputes these
  # same coordinates purely from (org, env), with no way to learn about an override.
  # An override here would silently strand those callers pointed at an address
  # where nothing lives, or make the resolver miss a Thunder that IS provisioned.
  local release ns host issuer chart secret_name thunder_port
  release="$(thunder_release_name "$org" "$ENV_NAME")"
  ns="$(thunder_namespace "$org" "$ENV_NAME")"
  host="$(thunder_host "$org" "$ENV_NAME")"
  issuer="$(thunder_issuer "$org" "$ENV_NAME")"
  chart="${THUNDER_CHART:-oci://ghcr.io/thunder-id/helm-charts/thunderid}"
  secret_name="${release}-system-client"
  thunder_port=8090

  # gateClient controls the scheme/port Thunder's own login ("gate") flow bakes
  # into its sign-in redirect URLs — it does NOT automatically follow TLS_ENABLED
  # the way thunder_issuer() above does, so it must be derived the same way here,
  # or every sign-in link keeps pointing at plain HTTP:8080 even once TLS_ENABLED
  # has switched the issuer/publicUrl over to HTTPS.
  local gate_client_port gate_client_scheme
  if [ "${TLS_ENABLED:-false}" = "true" ]; then
    gate_client_port=443
    gate_client_scheme=https
  else
    gate_client_port=8080
    gate_client_scheme=http
  fi

  local persistence_size="${PERSISTENCE_SIZE:-1Gi}"
  local storage_class="${STORAGE_CLASS:-}"
  local wait_timeout="${WAIT_TIMEOUT:-180s}"

  # Platform Thunder coordinates — CORS origin + trusted-issuer JWKS (HTTPS via port 8443).
  local pt_issuer pt_jwks pt_audience
  pt_issuer="${PLATFORM_THUNDER_ISSUER:-$(platform_thunder_issuer)}"
  pt_jwks="${PLATFORM_THUNDER_JWKS_URL:-$(platform_thunder_jwks_url)}"
  pt_audience="${PLATFORM_THUNDER_TOKEN_AUDIENCE:-amp}"

  # pt_issuer feeds both cors_origins below and trustedIssuer.issuer further down.
  # If TLS_ENABLED=true but PLATFORM_THUNDER_ISSUER was never explicitly set,
  # pt_issuer silently falls back to platform_thunder_issuer()'s plain-http
  # local-dev default, wiring an insecure origin/issuer into an otherwise
  # TLS-enabled install. That mismatch doesn't fail at startup, it just makes
  # every real token's iss claim silently stop matching later. Only check for
  # the unset case, not the scheme of an explicitly-passed value: a caller can
  # legitimately pass a plain-http PLATFORM_THUNDER_ISSUER alongside
  # TLS_ENABLED=true (e.g. platform Thunder reachable only via a local
  # port-forward while env-Thunder's own gateway routing still uses TLS).
  if [ "${TLS_ENABLED:-false}" = "true" ] && [ -z "${PLATFORM_THUNDER_ISSUER:-}" ]; then
    echo "❌ TLS_ENABLED=true but PLATFORM_THUNDER_ISSUER was not set — refusing to fall back to the"
    echo "   local-dev default ('$(platform_thunder_issuer)')."
    echo "   Set PLATFORM_THUNDER_ISSUER explicitly to platform Thunder's real issuer."
    exit 1
  fi

  # CORS origins for the AMP console reaching env-Thunder's own APIs directly
  # from the browser. localhost:3000/console.amp.localhost:8080 are quick-setup
  # (Rancher Desktop / amp-install-rancher.sh) console addresses — they don't
  # exist in a real deployment, so TLS_ENABLED=true (on-your-environment.mdx's
  # production flow) drops them and allows only the real platform Thunder
  # origin.
  local cors_origins
  if [ "${TLS_ENABLED:-false}" = "true" ]; then
    cors_origins="{${pt_issuer}}"
  else
    cors_origins="{http://localhost:3000,http://console.amp.localhost:8080,${pt_issuer}}"
  fi

  echo "=== Provisioning Thunder ID for environment '${ENV_NAME}' (org '${org}') ==="
  echo ""
  echo "  Release:   ${release}"
  echo "  Namespace: ${ns}"
  echo "  Issuer:    ${issuer}"
  echo "  Chart:     ${chart}"
  echo ""

  # Resolve chart version for OCI charts (local chart paths skip this). Pinned to the
  # upstream ThunderID release we've validated env-Thunder against, independent of
  # whatever version platform Thunder happens to run.
  local version_args=()
  if printf '%s' "$chart" | grep -q '^oci://'; then
    local chart_version="${CHART_VERSION:-0.45.0}"
    echo "📌 Using Thunder chart version: ${chart_version}"
    version_args=(--version "$chart_version")
  fi

  # Ensure the namespace exists (idempotent) so the secrets can live in it.
  kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  # Resolve the system-client secret: reuse an existing one (NO rotation), else
  # mint a unique one and store it as a K8s Secret in the env-Thunder namespace.
  local system_secret
  if system_secret="$(read_existing_secret "$ns" "$secret_name")" && [ -n "$system_secret" ]; then
    echo "🔐 Reusing existing system-client secret (${secret_name})"
  else
    system_secret="${SYSTEM_CLIENT_SECRET:-$(openssl rand -hex 24)}"
    kubectl create secret generic "$secret_name" -n "$ns" \
      --from-literal=client-secret="$system_secret" \
      --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    echo "🔐 Stored new system-client secret (${secret_name})"
  fi
  # Deliver the secret to AMS (see store_via_ams). client_id here must match
  # CLIENT_ID in render_system_client_bootstrap_script below (both "amp-system-client").
  if ! store_via_ams "$org" "$ENV_NAME" "amp-system-client" "$system_secret"; then
    exit 1
  fi

  # Resolve this env-Thunder's own native admin password: reuse an existing one (NO
  # rotation — logging in with the old password must keep working across re-runs),
  # else mint a unique one and store it as a K8s Secret in the env-Thunder namespace.
  local admin_secret_name="${release}-admin-credentials"
  local admin_password
  if admin_password="$(read_existing_secret "$ns" "$admin_secret_name" "password")" && [ -n "$admin_password" ]; then
    echo "🔐 Reusing existing admin password (${admin_secret_name})"
  else
    admin_password="${THUNDER_ADMIN_PASSWORD:-$(generate_admin_password)}"
    kubectl create secret generic "$admin_secret_name" -n "$ns" \
      --from-literal=password="$admin_password" \
      --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    echo "🔐 Stored new admin password (${admin_secret_name})"
  fi

  # Bootstrap ConfigMap: the ONLY custom bootstrap env-Thunder needs is the
  # amp-system-client OAuth2 app (see render_system_client_bootstrap_script above).
  # Pattern 2 (configMap.name + files) preserves ThunderID's own default bootstrap
  # scripts (org unit, default user schema, native Administrator role, etc.).
  local bootstrap_cm_name="${release}-bootstrap"
  kubectl create configmap "$bootstrap_cm_name" -n "$ns" \
    --from-literal="10-amp-system-client.sh=$(render_system_client_bootstrap_script "$system_secret")" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  echo "🔐 Bootstrap ConfigMap (${bootstrap_cm_name}) prepared"

  # Per-env overrides on top of upstream ThunderID chart defaults, using the
  # chart's own top-level values schema (no wrapper-chart value prefix).
  local set_args=(
    # Pin the K8s resource names to the release name (matches the naming every other
    # AMP component assumes, e.g. agent-manager-service/clients/thundersvc/naming.go's
    # "<release>-service" convention) instead of the chart's default fullname suffix.
    --set-string "fullnameOverride=${release}"
    --set-string "deployment.image.tag=${CHART_VERSION:-0.45.0}"
    # Single replica + writable root FS: required for SQLite (single-pod, local file DB).
    --set "deployment.replicaCount=1"
    --set "deployment.securityContext.readOnlyRootFilesystem=false"
    --set "hpa.enabled=false"
    --set "ingress.enabled=false"
    --set-string "configuration.server.publicUrl=${issuer}"
    --set "configuration.server.httpOnly=true"
    --set-string "configuration.jwt.issuer=${issuer}"
    --set-string "configuration.gateClient.hostname=${host}"
    --set "configuration.gateClient.port=${gate_client_port}"
    --set-string "configuration.gateClient.scheme=${gate_client_scheme}"
    --set "configuration.database.config.type=sqlite"
    --set "configuration.database.runtime.type=sqlite"
    --set "configuration.database.user.type=sqlite"
    --set "configuration.consent.database.type=sqlite"
    --set "configuration.cache.disabled=false"
    # CORS: allow the platform Thunder origin so its console can reach env-Thunder APIs
    # (plus quick-setup's local console addresses, only outside TLS_ENABLED — see
    # cors_origins above).
    --set "configuration.cors.allowedOrigins=${cors_origins}"
    --set "persistence.enabled=true"
    --set "persistence.size=${persistence_size}"
    # Native ThunderID superadmin (distinct from the AMP product's own admin user on
    # platform Thunder) — used to log into this env-Thunder's own /console. Password
    # is a per-env random secret (see admin_password resolution above), not "admin".
    --set-string "setup.admin.username=admin"
    --set-string "setup.admin.password=${admin_password}"
    --set-string "bootstrap.configMap.name=${bootstrap_cm_name}"
    --set-json 'bootstrap.configMap.files=["10-amp-system-client.sh"]'
  )
  if [ -n "$storage_class" ]; then
    set_args+=(--set-string "persistence.storageClass=${storage_class}")
  fi

  # ---------------------------------------------------------------------------
  # Trusted issuer: env-Thunder accepts tokens issued by platform Thunder.
  #
  # Flow:
  #   1. Fetch the self-signed CA cert that signed the platform Thunder TLS cert
  #      from cert-manager (or from PLATFORM_THUNDER_CA_PEM if injected by the
  #      caller — useful in tests/CI where cert-manager is not running).
  #   2. Fetch the Mozilla CA bundle (the same set shipped by Alpine / Debian
  #      ca-certificates packages) so the combined file is a complete trust store.
  #   3. Store the combined PEM bundle as a ConfigMap in the env-Thunder namespace,
  #      and queue --set trustedIssuer.issuer/jwksUrl/audience for the helm install
  #      below — that's the actual trust decision (which issuer to accept, and
  #      where to fetch its JWKS from); the CA bundle only exists so env-Thunder's
  #      Go TLS stack can reach that (HTTPS) JWKS URL in the first place.
  #   4. After install, mount the ConfigMap into the env-Thunder Deployment (via a
  #      post-install kubectl patch — NOT via the chart's declarativeResources
  #      support, see patch_ca_bundle_mount below for why) and set SSL_CERT_FILE
  #      to the combined file so Go's TLS stack trusts both commercial CAs and
  #      the self-signed CA.
  #
  # Set SKIP_CA_BUNDLE_TRUST=true to skip this whole flow when platform Thunder's
  # issuer is ALREADY backed by a real, publicly-trusted CA (e.g. a VM deployment
  # where Caddy terminates TLS with a Let's Encrypt cert — see
  # deployments/vm/lib-vm.sh) — the container's default system trust store already
  # trusts that CA, so there is nothing custom to fetch or mount. The trustedIssuer
  # --set-string args below (the actual trust decision) still apply either way;
  # only the custom-CA-bundle mechanics are skipped.
  # ---------------------------------------------------------------------------
  local ca_pem ca_cm_name=""
  if [ "${SKIP_CA_BUNDLE_TRUST:-false}" = "true" ]; then
    echo "🔐 SKIP_CA_BUNDLE_TRUST=true — platform Thunder's issuer is assumed to already be"
    echo "   backed by a publicly-trusted CA; skipping the custom CA bundle fetch/mount."
  else

  # Fetch platform Thunder CA. Missing cert is fatal to prevent silent auth failures.
  if ! ca_pem="$(platform_thunder_ca_cert)" || [ -z "$ca_pem" ]; then
    echo "❌ Platform Thunder CA cert is not available."
    echo "   Ensure cert-manager has issued amp-local-root-ca in the cert-manager namespace."
    echo "   Check status: kubectl get certificate -n cert-manager amp-local-root-ca"
    echo "   Alternatively, set PLATFORM_THUNDER_CA_PEM to inject the CA cert directly."
    exit 1
  fi

  # Fetch Mozilla root CA bundle. Appending our Root CA ensures the trust store
  # remains additive and compatible with any base image (Debian/Alpine).
  # The bundle is built on the operator's machine (not inside the pod), so we
  # cannot rely on the pod's /etc/ssl — we need a portable external source.
  local ca_bundle mozilla_bundle
  ca_cm_name="amp-thunder-platform-ca"

  # Short-circuit: if the ConfigMap already exists in the cluster (e.g. a re-run
  # or an idempotent apply), skip the 230KB download entirely.
  if kubectl get configmap "$ca_cm_name" -n "$ns" &>/dev/null 2>&1; then
    echo "🔐 CA bundle ConfigMap ${ns}/${ca_cm_name} already exists — skipping download"
    ca_bundle=""  # not used below when ConfigMap already present; set_args still queued
  else
    # Allow pre-downloading the bundle and pointing at it via MOZILLA_CA_BUNDLE.
    # Useful in air-gapped envs or when curl.se is unreachable.
    #   curl -fsSL https://curl.se/ca/cacert.pem -o /tmp/cacert.pem
    #   MOZILLA_CA_BUNDLE=/tmp/cacert.pem ENV_NAME=... bash add-environment-thunder.sh
    if [ -n "${MOZILLA_CA_BUNDLE:-}" ] && [ -f "$MOZILLA_CA_BUNDLE" ]; then
      echo "🔐 Using pre-downloaded Mozilla CA bundle from ${MOZILLA_CA_BUNDLE}"
      mozilla_bundle="$(cat "$MOZILLA_CA_BUNDLE")"
      if ! grep -q "BEGIN CERTIFICATE" <<< "$mozilla_bundle"; then
        echo "❌ MOZILLA_CA_BUNDLE file does not look like a PEM certificate bundle: ${MOZILLA_CA_BUNDLE}"
        exit 1
      fi
    else
      echo "🔐 Fetching Mozilla CA bundle from curl.se..."
      local tmp_bundle attempt download_ok
      tmp_bundle="$(mktemp)"
      download_ok=false
      for attempt in 1 2 3; do
        # Retry both download failures AND checksum mismatches — the old code only
        # retried the latter, causing the loop to be dead code on a network failure.
        if ! curl -fsSL --connect-timeout 30 https://curl.se/ca/cacert.pem -o "$tmp_bundle" 2>/dev/null; then
          if [ "$attempt" -lt 3 ]; then
            echo "⚠️  Download failed on attempt ${attempt}/3 — retrying in 5s..."
            sleep 5
            continue
          fi
          rm -f "$tmp_bundle"
          echo "❌ Could not fetch Mozilla CA bundle from https://curl.se/ca/cacert.pem after 3 attempts."
          echo "   Download it on a machine with internet access and set the path via MOZILLA_CA_BUNDLE:"
          echo "     curl -fsSL https://curl.se/ca/cacert.pem -o /tmp/cacert.pem"
          echo "     MOZILLA_CA_BUNDLE=/tmp/cacert.pem ENV_NAME=${ENV_NAME} ORG_NAME=${ORG_NAME} bash $(basename "$0")"
          exit 1
        fi
        if ! grep -q "BEGIN CERTIFICATE" "$tmp_bundle"; then
          if [ "$attempt" -lt 3 ]; then
            echo "⚠️  Downloaded file does not look like a PEM bundle on attempt ${attempt}/3 — retrying in 5s..."
            sleep 5
            continue
          fi
          rm -f "$tmp_bundle"
          echo "❌ Mozilla CA bundle download produced an unexpected response after 3 attempts."
          exit 1
        fi
        # Verify against the published checksum to detect download corruption.
        local expected_sha actual_sha
        if expected_sha="$(curl -fsSL --connect-timeout 15 https://curl.se/ca/cacert.pem.sha256 2>/dev/null | awk '{print $1}')" \
            && [ -n "$expected_sha" ]; then
          actual_sha="$(_sha256 "$tmp_bundle")"
          if [ "$expected_sha" != "$actual_sha" ]; then
            if [ "$attempt" -lt 3 ]; then
              echo "⚠️  Checksum mismatch on attempt ${attempt}/3 — retrying in 5s..."
              sleep 5
              continue
            fi
            rm -f "$tmp_bundle"
            echo "❌ Mozilla CA bundle checksum mismatch after 3 attempts — download may be corrupt."
            echo "   Expected: $expected_sha"
            echo "   Got:      $actual_sha"
            exit 1
          fi
          echo "   ✓ Checksum verified (SHA-256: ${actual_sha:0:16}...)"
        fi
        download_ok=true
        break
      done
      mozilla_bundle="$(cat "$tmp_bundle")"
      rm -f "$tmp_bundle"
      [ "$download_ok" = "true" ] || { echo "❌ CA bundle fetch failed unexpectedly"; exit 1; }
    fi

    ca_bundle="${mozilla_bundle}
${ca_pem}"

    # Store the combined bundle as a ConfigMap. Pass the bundle through a temp
    # file with --from-file rather than --from-literal: the Mozilla CA bundle is
    # ~230KB, which exceeds the Linux per-argument limit (MAX_ARG_STRLEN, 128KB)
    # and makes kubectl exit with "Argument list too long" before it emits any
    # YAML, so the piped `kubectl apply` then fails with "no objects passed to
    # apply". (macOS has a larger limit, so this only bites on Linux/CI.)
    local ca_bundle_file
    ca_bundle_file="$(mktemp)"
    printf '%s' "$ca_bundle" >"$ca_bundle_file"
    kubectl create configmap "$ca_cm_name" -n "$ns" \
      --from-file=ca-bundle.crt="$ca_bundle_file" \
      --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    rm -f "$ca_bundle_file"
    echo "🔐 Combined CA bundle (Mozilla + platform Thunder CA) stored in ${ns}/${ca_cm_name}"
  fi
  fi # SKIP_CA_BUNDLE_TRUST

  set_args+=(
    # Configure the trusted issuer endpoints. (CA bundle mounting is done via a
    # post-install kubectl patch below, NOT declarativeResources — see patch_ca_bundle_mount.)
    --set-string "configuration.server.security.trustedIssuer.issuer=${pt_issuer}"
    --set-string "configuration.server.security.trustedIssuer.jwksUrl=${pt_jwks}"
    --set-string "configuration.server.security.trustedIssuer.audience=${pt_audience}"
  )

  echo ""
  echo "📦 Installing Thunder (${release}) from ${chart}..."
  helm upgrade --install "$release" "$chart" \
    ${version_args[@]+"${version_args[@]}"} \
    --namespace "$ns" --create-namespace \
    "${set_args[@]}"

  if [ "${SKIP_CA_BUNDLE_TRUST:-false}" != "true" ]; then
    echo ""
    echo "🔐 Mounting platform CA bundle into the Deployment (post-install patch)..."
    patch_ca_bundle_mount "$release" "$ns" "$ca_cm_name"
  fi

  echo ""
  echo "🌐 Routing ${host}:8080 to ${release}..."
  apply_httproute "$release" "$ns" "$host" "$thunder_port"

  echo ""
  echo "⏳ Waiting for Thunder '${release}' to be ready..."
  if kubectl wait --for=condition=available --timeout="$wait_timeout" \
      deployment -l "app.kubernetes.io/instance=${release}" -n "$ns" 2>/dev/null; then
    echo "✅ Thunder is ready"
  else
    echo "⚠️  Thunder did not become ready in time — check: kubectl get pods -n ${ns}"
  fi

  echo ""
  echo "=== Thunder ID for '${ENV_NAME}' provisioned ==="
  echo ""
  echo "  Environment:     ${ENV_NAME}"
  echo "  Namespace:       ${ns}"
  echo "  Release:         ${release}"
  echo "  Chart:           ${chart} (${CHART_VERSION:-0.45.0})"
  echo "  Issuer:          ${issuer}"
  echo "  JWKS:            ${issuer}/oauth2/jwks"
  echo "  Trusted issuer:  ${pt_issuer}"
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "  Thunder ID Console — ${ENV_NAME}"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "  URL:      ${issuer}/console"
  echo "  Username: admin"
  echo "  Password: ${admin_password}"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "  ⚠️  Save this password now to access the '${ENV_NAME}' Environment ThunderID"
  echo "     console later, you'll need it, and it won't be shown again."
  echo ""
}

# Run main only when executed directly — not when sourced (e.g. by tests).
# BASH_SOURCE[0] is unset when the script is piped to bash (curl ... | bash);
# ${BASH_SOURCE[0]:-$0} falls back to $0 (which equals "bash") so the condition
# stays true and main runs, while sourced execution still sees the script filename.
if [ "${BASH_SOURCE[0]:-$0}" = "${0}" ]; then
  main "$@"
fi
