#!/usr/bin/env bash
# install-vm.sh — laptop-side orchestrator for "Agent Manager on a VM with Docker".
# Usage:
#   ./install-vm.sh --host <IP> --ssh-key <path> --version <amp-release>
#                   [--email <addr>] [--ssh-user <user>] [--no-external-gateways]
#
# --version: the amp/v* release to install (e.g. 0.15.0). Required — the charts
#   and manifests are pulled per-release; there is no sensible default.
#
# TLS is always Let's Encrypt, 443-only: certificates issue via the TLS-ALPN-01
# challenge inside the :443 handshake, so only inbound 443 is required (no port 80).
# The public :443 must reach Caddy as raw TCP (no TLS-terminating proxy in front).
set -euo pipefail

HOST="" SSH_KEY="" SSH_USER="root" ACME_EMAIL="" EXTERNAL_GATEWAYS="true"
AMP_VERSION="${VERSION:-}"   # amp/v* release to install; --version overrides the VERSION env
VM_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QS_DIR="$(cd "${VM_DIR}/.." && pwd)"
# shellcheck source=lib-vm.sh
source "${VM_DIR}/lib-vm.sh"

die() { printf '\033[0;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
log() { printf '\033[0;34m[install-vm]\033[0m %s\n' "$*"; }
require_value() { [[ -n "${2:-}" && "${2:-}" != --* ]] || die "$1 requires a value"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) require_value "$1" "${2:-}"; HOST="$2"; shift 2 ;;
    --ssh-key) require_value "$1" "${2:-}"; SSH_KEY="$2"; shift 2 ;;
    --ssh-user) require_value "$1" "${2:-}"; SSH_USER="$2"; shift 2 ;;
    --email) require_value "$1" "${2:-}"; ACME_EMAIL="$2"; shift 2 ;;
    --version) require_value "$1" "${2:-}"; AMP_VERSION="$2"; shift 2 ;;
    --no-external-gateways) EXTERNAL_GATEWAYS="false"; shift ;;
    -h|--help) grep '^#' "$0" | grep -v '^#!' | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown flag: $1" ;;
  esac
done

[[ -n "$HOST" ]] || die "--host <IP> is required"
# Public URLs are derived as *.amp.<host>.sslip.io, so the host must be an IPv4 literal.
[[ "$HOST" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || \
  die "--host must be an IPv4 address (got '${HOST}') — sslip.io hostnames embed the IP"
[[ -n "$AMP_VERSION" ]] || \
  die "--version <release> is required (an existing amp/v* tag, e.g. --version 0.15.0); see https://github.com/wso2/agent-manager/tags"
[[ -n "$SSH_KEY" ]] || die "--ssh-key <path> is required"
[[ -f "$SSH_KEY" ]] || die "ssh key not found: $SSH_KEY"

SSH=(ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "${SSH_USER}@${HOST}")
REMOTE_DIR="/opt/amp-quickstart"

remote() { "${SSH[@]}" "$@"; }
remote_run() {
  # Runs remote-install.sh with the install config in the remote env.
  local phase="$1"; shift || true
  # VERSION is only needed by the install phase. Crucially, do NOT export it during
  # bootstrap: get.docker.com (and other piped installers) read $VERSION as the
  # Docker version to install and fail on the AMP release string.
  local ver_env=""
  [[ "$phase" == "install" ]] && ver_env="VERSION='${AMP_VERSION}'"
  remote "sudo VM_IP='${HOST}' EXTERNAL_GATEWAYS='${EXTERNAL_GATEWAYS}' \
    ACME_EMAIL='${ACME_EMAIL}' ${ver_env} \
    bash \"${REMOTE_DIR}/vm/remote-install.sh\" \"${phase}\" \"$*\""
}

log "Copying installer to ${SSH_USER}@${HOST}:${REMOTE_DIR}"
# tar-over-ssh rather than rsync: tar is universally present, whereas rsync must
# be installed on BOTH ends and minimal VM images often lack it (and we can't
# install it via bootstrap, which runs after this copy).
remote "sudo mkdir -p '${REMOTE_DIR}'"
tar -C "${QS_DIR}" -czf - . | remote "sudo tar -C '${REMOTE_DIR}' -xzf -"

log "Phase 1/3: bootstrap (Docker + firewall)"
remote_run bootstrap

# Preflight: TLS-ALPN-01 needs inbound :443 reachable from the internet.
log "Phase 2/3: preflight — verifying 443 reachable from the internet"
remote_run preflight 443 &
# Retry within the remote listener's window instead of a single check after a
# fixed sleep — a slow VM/SSH path may not have bound the socket yet.
reachable="false"
for _ in $(seq 1 12); do
  if nc -z -w 1 "$HOST" 443; then reachable="true"; break; fi
  sleep 1
done
if [[ "$reachable" != "true" ]]; then
  wait || true
  die "Port 443 is NOT reachable from this machine.
  Open inbound 443/tcp in your cloud security group / NACL.
  The public :443 must reach Caddy as raw TCP (no TLS-terminating proxy in front)."
fi
wait || true
log "  :443 reachable"

log "Phase 3/3: install Agent Manager + start Caddy (this takes 8-15 min)"
remote_run install

log "Done. Access URLs:"
cat <<EOF

  Console:   https://$(vm_host console "$HOST")
  API:       https://$(vm_host api "$HOST")
  Thunder:   https://$(vm_host thunder "$HOST")
  Observer:  https://$(vm_host observer "$HOST")
  OTel ingest: https://$(vm_host gateway "$HOST")/otel
  Deployed agents: https://<org>-<project>.agents.${HOST}.sslip.io/...
EOF
[[ "$EXTERNAL_GATEWAYS" == "true" ]] && echo "  Gateway control plane: https://$(vm_host cp "$HOST")  (connect external gateways here; registration token is secret-bearing)"
