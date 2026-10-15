#!/usr/bin/env bash
# k3d-install-subscription.sh — one-command install of the WSO2-supported build
# of Agent Manager on a local k3d cluster, for WSO2 subscription customers.
#
# Attached as its own release asset (like k3d-install.sh) and piped to bash:
#   curl -fsSL <URL>/k3d-install-subscription.sh | bash -s -- --project <project>
#
# <project> is your registry project: wso2-agent-manager for a subscription, or
# wso2-agent-manager-evaluations for an evaluation. The installer itself comes
# from this release's public bundle on GitHub, as with k3d-install.sh; only the
# Helm charts and Agent Manager images come from registry.wso2.com/<project>,
# pulled by helm and by the cluster with your registry credentials. The newest
# (or the given) update level of the release is found from the chart versions
# published to the project, so new update levels need no new copy of this script.
#
# Credentials come from AMP_REGISTRY_USERNAME and AMP_REGISTRY_PASSWORD, from a
# file named by AMP_REGISTRY_CREDENTIALS_FILE holding both, or from a prompt on
# the terminal. They are never taken as arguments: argv is readable by every user
# through ps, and lands in shell history.
set -euo pipefail

# Stamped to the release version at build time (see .github/scripts/update-install-helpers.sh).
# Support updates of a release are published as <release>.<level>.
DEFAULT_VERSION="1.0.0"
REGISTRY_HOST="${AMP_SUBSCRIPTION_REGISTRY:-registry.wso2.com}"
PROJECTS=(wso2-agent-manager wso2-agent-manager-evaluations)
# Charts sit under this path in each project, apart from the images.
CHARTS_SUBPATH="helm-charts"
REPO="${AMP_REPO:-wso2/agent-manager}"
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
  --download-only  Check the credentials, resolve the update level, download
                   the installer, print where it is, and stop without installing

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
  required=(helm curl tar)
fi
missing=()
for tool in "${required[@]}"; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done
[[ ${#missing[@]} -eq 0 ]] || \
  die "missing required tools: ${missing[*]} — see https://wso2.com/agent-platform/docs/v${VERSION}/guides/on-k3d/#prerequisites"

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
# Every request to the registry goes through helm, with the token on stdin and a
# throwaway registry config, so the caller's own helm logins are left untouched.
# The bundled installer signs helm in again for the install itself.
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
export HELM_REGISTRY_CONFIG="${WORK}/helm-registry.json"
HELM_REGISTRY_FLAGS=()
if [[ "${AMP_REGISTRY_ALLOW_INSECURE:-false}" == true ]]; then
  HELM_REGISTRY_FLAGS+=(--plain-http)   # test registries only
fi

log "Signing in to ${REGISTRY_HOST}"
if ! printf '%s' "$AMP_REGISTRY_PASSWORD" | helm registry login "$REGISTRY_HOST" \
    --username "$AMP_REGISTRY_USERNAME" --password-stdin ${HELM_REGISTRY_FLAGS[@]+"${HELM_REGISTRY_FLAGS[@]}"} >/dev/null 2>"$WORK/err"; then
  die "${REGISTRY_HOST} rejected the registry credentials: $(tail -1 "$WORK/err")"
fi

# --- Pick the update ----------------------------------------------------------
# Update level N of a release is chart version <release>-N, so the newest level
# is the highest chart version within the release. --devel is what lets helm
# consider those versions at all, and the range keeps this copy of the script on
# its own release when a later release's charts are published to the project.
CHART_REF="oci://${REGISTRY_HOST}/${PROJECT}/${CHARTS_SUBPATH}/wso2-agent-manager"
IFS=. read -r major minor patch <<<"$VERSION"
if [[ -z "$LEVEL" ]]; then
  constraint=">=${VERSION}-0 <${major}.${minor}.$((patch + 1))-0"
else
  constraint="${VERSION}-${LEVEL}"
fi
if ! helm show chart "$CHART_REF" --devel --version "$constraint" \
    ${HELM_REGISTRY_FLAGS[@]+"${HELM_REGISTRY_FLAGS[@]}"} >"$WORK/chart.yaml" 2>"$WORK/err"; then
  if grep -qiE 'not found|could not locate|no matching|name unknown|not known to registry' "$WORK/err"; then
    if [[ -n "$LEVEL" ]]; then
      die "update level ${LEVEL} of ${VERSION} is not published to ${PROJECT}"
    fi
    die "no update of ${VERSION} is published to ${PROJECT} yet"
  fi
  if grep -qiE 'unauthori|denied|forbidden|401|403' "$WORK/err"; then
    die "the credentials have no access to ${PROJECT}: check the registry project and the token"
  fi
  die "could not read ${CHART_REF}: $(tail -1 "$WORK/err")"
fi
CHART_VERSION="$(sed -n 's/^version:[[:space:]]*//p' "$WORK/chart.yaml" | head -1)"
[[ "$CHART_VERSION" =~ ^${VERSION//./\\.}-([0-9]+)$ ]] || \
  die "unexpected chart version '${CHART_VERSION}' for release ${VERSION} in ${PROJECT}"
LEVEL="${BASH_REMATCH[1]}"
TAG="${VERSION}.${LEVEL}"
log "Using ${PROJECT} update ${TAG} (charts ${CHART_VERSION})"

# --- Download the installer ---------------------------------------------------
# The same public release bundle k3d-install.sh uses; it holds no credentials and
# is the same for every update level of the release.
BUNDLE_URL="${AMP_BUNDLE_URL:-https://github.com/${REPO}/releases/download/amp/v${VERSION}/wso2-agent-manager-${VERSION}.tar.gz}"
DIST_DIR="${AMP_HOME}/subscription/${PROJECT}/${TAG}"
# Start from an empty directory, so files left by an earlier download cannot
# mix with this bundle or satisfy the checks below on its behalf.
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"
BUNDLE="${DIST_DIR}/wso2-agent-manager-${VERSION}.tar.gz"
log "Downloading install bundle (release ${VERSION})"
log "  ${BUNDLE_URL}"
curl -fsSL --retry 5 --retry-all-errors -o "$BUNDLE" "$BUNDLE_URL" \
  || die "failed to download the install bundle — check the release tag amp/v${VERSION} exists (or set AMP_BUNDLE_URL)"
tar -xzf "$BUNDLE" -C "$DIST_DIR" || die "failed to unpack the install bundle"
QUICK_START_DIR="${DIST_DIR}/deployments/quick-start"
[[ -f "${QUICK_START_DIR}/install.sh" ]] || die "the install bundle is missing deployments/quick-start/install.sh"
# Installing from a private registry needs the installer's registry support,
# including the helm sign-in for private charts. A release bundle without it
# would fail part way, so stop here instead.
grep -q 'amp_registry_helm_login' "${DIST_DIR}/deployments/scripts/lib-registry-auth.sh" 2>/dev/null || \
  die "the ${VERSION} install bundle cannot install from a private registry; it needs a newer release of the installer"
log "Installer unpacked in ${DIST_DIR}"

if [[ "$DOWNLOAD_ONLY" == true ]]; then
  log "Download only: to install, run"
  log "  cd ${QUICK_START_DIR} && AMP_IMAGE_REGISTRY=${REGISTRY_HOST}/${PROJECT} HELM_CHART_REGISTRY=${REGISTRY_HOST}/${PROJECT}/${CHARTS_SUBPATH} VERSION=${CHART_VERSION} ./install.sh"
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

# Point the installer at the project for this update. These are set explicitly,
# so values left in the shell from an earlier guided install cannot redirect it.
# The installer takes the credentials from here for the node's registry
# configuration, the image pull secrets and its own helm sign-in.
export AMP_IMAGE_REGISTRY="${REGISTRY_HOST}/${PROJECT}"
export HELM_CHART_REGISTRY="${REGISTRY_HOST}/${PROJECT}/${CHARTS_SUBPATH}"
export VERSION="$CHART_VERSION"
export AMP_REGISTRY_USERNAME AMP_REGISTRY_PASSWORD
unset AMP_REGISTRY_CREDENTIALS_FILE HELM_REGISTRY_CONFIG

log "Installing Agent Manager ${TAG} on k3d (~15-20 minutes)"
cd "$QUICK_START_DIR"
bash ./install.sh
