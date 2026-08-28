#!/usr/bin/env bash
# bootstrap.sh — one-command entry point for installing Agent Manager on a VM.
#
# Fetched and piped to bash; it downloads ONE versioned install bundle (a single
# release asset — no git clone, no per-file raw.githubusercontent fetches, which
# GitHub rate-limits per IP) and runs the selected installer from it.
#
# Usage (on the VM, as root):
#   curl -fsSL <URL>/bootstrap.sh | sudo bash -s -- simple \
#       --host <PUBLIC_IP> --version <release> [--email <addr>] [--no-external-gateways]
#   curl -fsSL <URL>/bootstrap.sh | sudo bash -s -- advanced --config <amp-config.env>
#
# simple reads the release from --version; advanced reads AMP_VERSION from --config
# (the operator pins it in one place). Override the bundle location with
# AMP_BUNDLE_URL (and the repo with AMP_REPO) for testing against a fork/pre-release.
set -euo pipefail

REPO="${AMP_REPO:-wso2/agent-manager}"

log() { printf '\033[0;34m[bootstrap]\033[0m %s\n' "$*"; }
die() { printf '\033[0;31m[bootstrap] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: curl -fsSL <url>/bootstrap.sh | sudo bash -s -- <simple|advanced> [installer flags]

  simple    --host <PUBLIC_IP> --version <release> [--email <addr>] [--no-external-gateways]
  advanced  --config <amp-config.env> [--dry-run]   # release read from AMP_VERSION in the config
EOF
}

MODE="${1:-}"; shift 2>/dev/null || true
case "$MODE" in
  simple|advanced) ;;
  ""|-h|--help) usage; exit 0 ;;
  *) die "first argument must be 'simple' or 'advanced' (got '${MODE}')" ;;
esac

[[ "$(id -u)" -eq 0 ]] || \
  die "run with sudo — the installer opens the firewall and creates the cluster: curl -fsSL <url> | sudo bash -s -- <simple|advanced> ..."

args=("$@")

# Discover the requested release: --version from the args, else AMP_VERSION from --config.
VERSION="${VERSION:-}" CONFIG=""
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[$i]}" in
    --version) VERSION="${args[$((i + 1))]:-}" ;;
    --config)  CONFIG="${args[$((i + 1))]:-}" ;;
  esac
done
if [[ -z "$VERSION" && -n "$CONFIG" && -f "$CONFIG" ]]; then
  # Strip the inline comment (the --init template annotates this line) before trimming.
  VERSION="$(grep -E '^[[:space:]]*AMP_VERSION=' "$CONFIG" | head -n1 | cut -d= -f2- | sed 's/#.*//' | tr -d '" ')"
fi
[[ -n "$VERSION" ]] || \
  die "could not determine the release version — pass --version <release> (simple) or set AMP_VERSION in --config (advanced)"

BUNDLE_URL="${AMP_BUNDLE_URL:-https://github.com/${REPO}/releases/download/amp/v${VERSION}/wso2-agent-manager-${VERSION}.tar.gz}"

command -v curl >/dev/null 2>&1 || die "curl is required to download the install bundle"
command -v tar  >/dev/null 2>&1 || die "tar is required to unpack the install bundle"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

log "Downloading install bundle (release ${VERSION})"
log "  ${BUNDLE_URL}"
curl -fsSL --retry 5 --retry-all-errors -o "${WORKDIR}/bundle.tar.gz" "$BUNDLE_URL" \
  || die "failed to download the install bundle — check the release tag amp/v${VERSION} exists (or set AMP_BUNDLE_URL)"
tar -xzf "${WORKDIR}/bundle.tar.gz" -C "$WORKDIR" || die "failed to unpack the install bundle"

# The bundle reproduces the repo layout (deployments/…); find the installer within it.
VM_DIR="$(find "$WORKDIR" -type d -path '*/deployments/vm' -print -quit)"
[[ -n "$VM_DIR" ]] || die "install bundle is missing deployments/vm"

case "$MODE" in
  simple)   installer="${VM_DIR}/install-vm.sh" ;;
  advanced) installer="${VM_DIR}/install-advanced.sh" ;;
esac
[[ -f "$installer" ]] || die "installer not found in bundle: ${installer}"

log "Running ${MODE} installer from the bundle"
# Run as a child (not exec) so the EXIT trap still fires and cleans up $WORKDIR; the
# child inherits the invoker's working directory, so a relative --config path resolves.
bash "$installer" "${args[@]}"
