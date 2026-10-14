#!/usr/bin/env bash
# k3d-install-subscription.sh — one-command install of the WSO2-supported build
# of Agent Manager on a local k3d cluster, for WSO2 subscription customers.
#
# Attached as its own release asset (like k3d-install.sh) and piped to bash:
#   curl -fsSL <URL>/k3d-install-subscription.sh | bash -s -- --project <project>
#
# <project> is the registry project named in the email that carried your
# registry credentials: wso2-agent-manager for a subscription, or
# wso2-agent-manager-evaluations for an evaluation. Where k3d-install.sh installs
# the community build from the GitHub release, this downloads the install bundle
# for the newest (or the given) update level from registry.wso2.com/<project>
# with those credentials, and runs it, so every chart and image comes from that
# project.
#
# Credentials come from AMP_REGISTRY_USERNAME and AMP_REGISTRY_PASSWORD, from a
# file named by AMP_REGISTRY_CREDENTIALS_FILE holding both, or from a prompt on
# the terminal. They are never taken as arguments: argv is readable by every user
# through ps, and lands in shell history, and they are only ever sent over HTTPS
# (AMP_REGISTRY_ALLOW_INSECURE=true lifts that for a test registry).
set -euo pipefail

# Stamped to the release version at build time (see .github/scripts/update-install-helpers.sh).
# Support updates of a release are published as <release>.<level>.
DEFAULT_VERSION="1.0.0"
REGISTRY_URL="${AMP_SUBSCRIPTION_REGISTRY_URL:-https://registry.wso2.com}"
REGISTRY_URL="${REGISTRY_URL%/}"
REGISTRY_HOST="${REGISTRY_URL#*://}"
PROJECTS=(wso2-agent-manager wso2-agent-manager-evaluations)
BUNDLE_REPO="amp-install-bundle"
AMP_HOME="${AMP_HOME:-${HOME}/.wso2-agent-manager}"

log() { printf '\033[0;34m[k3d-install]\033[0m %s\n' "$*"; }
warn() { printf '\033[0;33m[k3d-install] WARNING:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[0;31m[k3d-install] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: curl -fsSL <url>/k3d-install-subscription.sh | bash -s -- --project <project> [options]

Creates a k3d cluster named amp-local and installs the WSO2-supported build of
OpenChoreo and Agent Manager on it, from your WSO2 registry project. Needs Docker
(running), k3d, kubectl, helm, curl and lsof on the host.

  --project        wso2-agent-manager (subscription) or
                   wso2-agent-manager-evaluations (evaluation)
  --update-level   Update level to install (default: the newest published)
  --version        Release the update levels belong to (default: the release
                   this script shipped with)
  --download-only  Download and verify the install bundle, print where it is,
                   and stop without installing

Registry credentials: AMP_REGISTRY_USERNAME and AMP_REGISTRY_PASSWORD, or
AMP_REGISTRY_CREDENTIALS_FILE, or a prompt on the terminal.
EOF
}

# --- Arguments ----------------------------------------------------------------
PROJECT="" LEVEL="" DOWNLOAD_ONLY=false
VERSION="$DEFAULT_VERSION"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) PROJECT="${2:?--project requires a value}"; shift 2 ;;
    --update-level) LEVEL="${2:?--update-level requires a value}"; shift 2 ;;
    --version) VERSION="${2:?--version requires a value}"; shift 2 ;;
    --download-only) DOWNLOAD_ONLY=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done
VERSION="${VERSION#v}"
# Matched by suffix, not the literal placeholder, which the release stamping rewrites.
[[ "$VERSION" != *-dev ]] || \
  die "this copy of the script is not tied to a release — pass --version <release>"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "--version must look like 1.0.0 (got '${VERSION}')"
[[ -z "$LEVEL" || "$LEVEL" =~ ^(0|[1-9][0-9]*)$ ]] || die "--update-level must be a number (got '${LEVEL}')"

# A piped script has the script itself on stdin, so prompts read the terminal.
have_tty() { [[ -r /dev/tty ]] && { : </dev/tty; } 2>/dev/null; }

if [[ -z "$PROJECT" ]]; then
  have_tty || die "--project is required: one of ${PROJECTS[*]}"
  printf 'Which registry project did WSO2 give you?\n  1) wso2-agent-manager              (subscription)\n  2) wso2-agent-manager-evaluations  (evaluation)\nChoose 1 or 2: ' >&2
  read -r choice </dev/tty
  case "$choice" in
    1) PROJECT="${PROJECTS[0]}" ;;
    2) PROJECT="${PROJECTS[1]}" ;;
    *) die "expected 1 or 2" ;;
  esac
fi
case " ${PROJECTS[*]} " in
  *" ${PROJECT} "*) ;;
  *) die "unknown project '${PROJECT}': expected one of ${PROJECTS[*]}" ;;
esac

# --- Prerequisites (the same checks as k3d-install.sh) ------------------------
log "Checking prerequisites..."
if [[ "$DOWNLOAD_ONLY" == false ]]; then
  command -v docker >/dev/null 2>&1 || \
    die "Docker is required but was not found on PATH: https://docs.docker.com/get-docker/"
  if ! docker info >/dev/null 2>&1; then
    msg="Docker is installed but the daemon is not reachable (docker info failed)."
    if [[ "$(uname -s)" == "Darwin" ]]; then
      msg+=$'\n'"On macOS, start Colima with a dedicated profile:"
      msg+=$'\n'"  colima start --profile agent-manager --vm-type=vz --vz-rosetta --network-address --cpu 4 --memory 8"
    fi
    die "$msg"
  fi
  required=(k3d kubectl helm curl tar lsof)
else
  required=(curl tar)
fi
missing=()
for tool in "${required[@]}"; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done
[[ ${#missing[@]} -eq 0 ]] || \
  die "missing required tools: ${missing[*]} — see https://wso2.com/agent-platform/docs/v${VERSION}/guides/on-k3d/#prerequisites"
if command -v sha256sum >/dev/null 2>&1; then sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else die "need sha256sum or shasum to verify the download"
fi

# --- Credentials --------------------------------------------------------------
# The file is parsed, not sourced, so a stray line in it cannot run anything.
if [[ -n "${AMP_REGISTRY_CREDENTIALS_FILE:-}" ]] && [[ -z "${AMP_REGISTRY_USERNAME:-}" || -z "${AMP_REGISTRY_PASSWORD:-}" ]]; then
  [[ -f "$AMP_REGISTRY_CREDENTIALS_FILE" ]] || die "credentials file not found: ${AMP_REGISTRY_CREDENTIALS_FILE}"
  while IFS='=' read -r key value || [[ -n "$key" ]]; do
    value="${value%$'\r'}"; value="${value#\'}"; value="${value%\'}"; value="${value#\"}"; value="${value%\"}"
    case "$key" in
      AMP_REGISTRY_USERNAME) [[ -n "${AMP_REGISTRY_USERNAME:-}" ]] || AMP_REGISTRY_USERNAME="$value" ;;
      AMP_REGISTRY_PASSWORD) [[ -n "${AMP_REGISTRY_PASSWORD:-}" ]] || AMP_REGISTRY_PASSWORD="$value" ;;
    esac
  done < "$AMP_REGISTRY_CREDENTIALS_FILE"
fi
if [[ -z "${AMP_REGISTRY_USERNAME:-}" || -z "${AMP_REGISTRY_PASSWORD:-}" ]]; then
  have_tty || die "no registry credentials: set AMP_REGISTRY_USERNAME and AMP_REGISTRY_PASSWORD, or AMP_REGISTRY_CREDENTIALS_FILE"
  if [[ -z "${AMP_REGISTRY_USERNAME:-}" ]]; then
    printf 'Registry username for %s: ' "$REGISTRY_HOST" >&2
    IFS= read -r AMP_REGISTRY_USERNAME </dev/tty
  fi
  if [[ -z "${AMP_REGISTRY_PASSWORD:-}" ]]; then
    printf 'Registry token for %s: ' "$REGISTRY_HOST" >&2
    IFS= read -rs AMP_REGISTRY_PASSWORD </dev/tty
    printf '\n' >&2
  fi
fi
[[ -n "${AMP_REGISTRY_USERNAME:-}" && -n "${AMP_REGISTRY_PASSWORD:-}" ]] || die "registry username and token are both required"

# --- Registry access ----------------------------------------------------------
# Credentials and tokens reach curl through a config on stdin, never argv, and
# only over HTTPS: every credential-bearing request is limited to it, so neither
# a plain-HTTP registry URL nor a plain-HTTP token service can receive them.
PROTO="=https"
if [[ "${AMP_REGISTRY_ALLOW_INSECURE:-false}" == true ]]; then
  PROTO="=http,https"
elif [[ "$REGISTRY_URL" != https://* ]]; then
  die "the registry URL must use HTTPS (got ${REGISTRY_URL}); set AMP_REGISTRY_ALLOW_INSECURE=true only for a test registry"
fi
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
quote() { local s="${1//\\/\\\\}"; printf '"%s"' "${s//\"/\\\"}"; }

# The registry says how to authenticate in its /v2/ challenge: a bearer token
# from a token service (Harbor), or basic auth on every request.
challenge="$(curl -sS --proto "$PROTO" -o /dev/null -D - "${REGISTRY_URL}/v2/" 2>/dev/null | tr -d '\r' \
  | awk 'tolower($1) == "www-authenticate:" { $1 = ""; sub(/^ /, ""); print; exit }')" || true
AUTH_CONFIG="$WORK/auth.curlrc"
case "$challenge" in
  [Bb]earer*)
    realm="$(sed -n 's/.*realm="\([^"]*\)".*/\1/p' <<<"$challenge")"
    service="$(sed -n 's/.*service="\([^"]*\)".*/\1/p' <<<"$challenge")"
    [[ -n "$realm" ]] || die "could not read the token service from ${REGISTRY_HOST}"
    token="$(printf 'user = %s\n' "$(quote "${AMP_REGISTRY_USERNAME}:${AMP_REGISTRY_PASSWORD}")" \
      | curl -sS --proto "$PROTO" -K - -G "$realm" --data-urlencode "service=${service}" \
          --data-urlencode "scope=repository:${PROJECT}/${BUNDLE_REPO}:pull" 2>/dev/null \
      | grep -oE '"(token|access_token)"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 \
      | sed 's/.*"\([^"]*\)"$/\1/')" || true
    [[ -n "$token" ]] || die "${REGISTRY_HOST} did not issue a token: check the registry username and token"
    printf 'header = %s\n' "$(quote "Authorization: Bearer ${token}")" > "$AUTH_CONFIG"
    ;;
  [Bb]asic*)
    printf 'user = %s\n' "$(quote "${AMP_REGISTRY_USERNAME}:${AMP_REGISTRY_PASSWORD}")" > "$AUTH_CONFIG"
    ;;
  *)
    die "${REGISTRY_URL} did not answer as a container registry; check your network or proxy"
    ;;
esac
chmod 600 "$AUTH_CONFIG"

# registry_get <path> <out-file> [accept]: prints the HTTP status. Blob downloads
# may redirect to storage; only HTTPS redirects are followed, so the credentials
# can never be replayed over plain HTTP.
registry_get() {
  local args=(-sS -L --proto "$PROTO" --proto-redir "=https" -K "$AUTH_CONFIG" -o "$2" -w '%{http_code}')
  [[ -n "${3:-}" ]] && args+=(-H "Accept: $3")
  curl "${args[@]}" "${REGISTRY_URL}/v2/${PROJECT}/${BUNDLE_REPO}/$1" 2>/dev/null || true
}
access_error() {
  case "$1" in
    401|403) die "the credentials have no access to ${PROJECT}: check the project name in your email and the token" ;;
    404) die "no install bundle is published to ${PROJECT}${2:+ for update ${2}}" ;;
    *) die "${REGISTRY_HOST} answered HTTP ${1} for ${PROJECT}/${BUNDLE_REPO}" ;;
  esac
}

# --- Pick the update ----------------------------------------------------------
# The bundle is published after an update's images and charts, so its tags are
# exactly the complete updates. Other tags in the repository (signatures) are
# skipped by matching <release>.<level> only.
code="$(registry_get "tags/list?n=1000" "$WORK/tags.json")"
[[ "$code" == 200 ]] || access_error "$code"
LEVELS="$(grep -oE "\"${VERSION//./\\.}\.[0-9]+\"" "$WORK/tags.json" | tr -d '"' | sed "s/^${VERSION//./\\.}\.//" | sort -n || true)"
[[ -n "$LEVELS" ]] || die "no update of ${VERSION} is published to ${PROJECT} yet"
if [[ -z "$LEVEL" ]]; then
  LEVEL="$(tail -1 <<<"$LEVELS")"
elif ! grep -qx "$LEVEL" <<<"$LEVELS"; then
  die "update level ${LEVEL} of ${VERSION} is not published to ${PROJECT}; available: $(tr '\n' ' ' <<<"$LEVELS")"
fi
TAG="${VERSION}.${LEVEL}"
CHART_VERSION="${VERSION}-${LEVEL}"
log "Using ${PROJECT} update ${TAG} (charts ${CHART_VERSION})"

# --- Download and verify the bundle -------------------------------------------
code="$(registry_get "manifests/${TAG}" "$WORK/manifest.json" "application/vnd.oci.image.manifest.v1+json")"
[[ "$code" == 200 ]] || access_error "$code" "$TAG"
# The bundle is the one layer of this media type. Anything other than exactly one
# means the artifact is not what this script expects, so it stops rather than
# unpacking whichever blob a looser match would pick.
BUNDLE_MEDIA_TYPE="application/vnd.oci.image.layer.v1.tar+gzip"
# A descriptor may carry a "urls" list; it is dropped first so its brackets
# cannot end the layers list early.
bundle_layers="$(tr -d '\n\r\t ' < "$WORK/manifest.json" \
  | sed 's/"urls":\[[^]]*\],\{0,1\}//g' \
  | sed -n 's/.*"layers":\[\([^]]*\)\].*/\1/p' \
  | awk '{ gsub(/\},\{/, "}\n{"); print }' \
  | grep -F "\"mediaType\":\"${BUNDLE_MEDIA_TYPE}\"" || true)"
[[ -n "$bundle_layers" && "$(grep -c . <<<"$bundle_layers")" -eq 1 ]] || \
  die "the ${TAG} bundle manifest does not have exactly one ${BUNDLE_MEDIA_TYPE} layer"
LAYER="$(sed -n 's/.*"digest":"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' <<<"$bundle_layers")"
[[ -n "$LAYER" ]] || die "the ${TAG} bundle layer has no valid digest"

DIST_DIR="${AMP_HOME}/subscription/${PROJECT}/${TAG}"
mkdir -p "$DIST_DIR"
BUNDLE="${DIST_DIR}/wso2-agent-manager-${CHART_VERSION}.tar.gz"
log "Downloading install bundle ${REGISTRY_HOST}/${PROJECT}/${BUNDLE_REPO}:${TAG}"
code="$(registry_get "blobs/${LAYER}" "$BUNDLE")"
[[ "$code" == 200 ]] || access_error "$code" "$TAG"
[[ "sha256:$(sha256 "$BUNDLE")" == "$LAYER" ]] || { rm -f "$BUNDLE"; die "the downloaded bundle does not match its digest; try again"; }
tar -xzf "$BUNDLE" -C "$DIST_DIR" || die "failed to unpack the install bundle"
QUICK_START_DIR="${DIST_DIR}/deployments/quick-start"
[[ -f "${QUICK_START_DIR}/install.sh" ]] || die "the install bundle is missing deployments/quick-start/install.sh"
# The bundle's installer defaults name where its charts and images come from,
# and its manifest records the project it was built for. A bundle built for a
# different project would install from there, so refuse it.
grep -q "\"registry\": *\"${REGISTRY_HOST}/${PROJECT}\"" "${DIST_DIR}/MANIFEST.json" 2>/dev/null || \
  die "the ${TAG} bundle was not built for ${REGISTRY_HOST}/${PROJECT}"
log "Bundle verified and unpacked in ${DIST_DIR}"

if [[ "$DOWNLOAD_ONLY" == true ]]; then
  log "Download only: to install, run"
  log "  cd ${QUICK_START_DIR} && ./install.sh"
  log "with AMP_REGISTRY_USERNAME and AMP_REGISTRY_PASSWORD (or AMP_REGISTRY_CREDENTIALS_FILE) set."
  exit 0
fi

# --- Install ------------------------------------------------------------------
# k3d's DNS fix rewrites the node's resolv.conf to the Docker host gateway, which
# does not resolve anything on Colima; the guided install asks Colima users to
# turn it off, so do the same here.
if [[ -z "${K3D_FIX_DNS:-}" ]] && docker info --format '{{.Name}}' 2>/dev/null | grep -qi colima; then
  log "Colima detected — setting K3D_FIX_DNS=0"
  export K3D_FIX_DNS=0
fi

# Where the charts and images come from, and at which version, is stamped into
# the bundle's installer defaults when the bundle is published, so it always
# matches how that update was published. Clear any values left in the shell
# from an earlier guided install so those defaults apply. The bundled installer
# takes the credentials from here for the node's registry configuration, the
# pull secrets and the helm login.
unset AMP_IMAGE_REGISTRY HELM_CHART_REGISTRY VERSION
export AMP_REGISTRY_USERNAME AMP_REGISTRY_PASSWORD
unset AMP_REGISTRY_CREDENTIALS_FILE

log "Installing Agent Manager ${TAG} on k3d (~15-20 minutes)"
cd "$QUICK_START_DIR"
bash ./install.sh
