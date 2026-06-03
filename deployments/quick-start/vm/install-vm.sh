#!/usr/bin/env bash
# install-vm.sh — laptop-side orchestrator for "Agent Manager on a VM with Docker".
# Usage:
#   ./install-vm.sh --host <IP> --ssh-key <path> [--email <addr>]
#                   [--tls letsencrypt|http] [--ssh-user <user>]
#                   [--no-external-gateways]
set -euo pipefail

HOST="" SSH_KEY="" SSH_USER="root" TLS_MODE="letsencrypt" ACME_EMAIL="" EXTERNAL_GATEWAYS="true"
VM_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QS_DIR="$(cd "${VM_DIR}/.." && pwd)"
# shellcheck source=lib-vm.sh
source "${VM_DIR}/lib-vm.sh"

die() { printf '\033[0;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
log() { printf '\033[0;34m[install-vm]\033[0m %s\n' "$*"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="$2"; shift 2 ;;
    --ssh-key) SSH_KEY="$2"; shift 2 ;;
    --ssh-user) SSH_USER="$2"; shift 2 ;;
    --email) ACME_EMAIL="$2"; shift 2 ;;
    --tls) TLS_MODE="$2"; shift 2 ;;
    --no-external-gateways) EXTERNAL_GATEWAYS="false"; shift ;;
    -h|--help) grep '^#' "$0" | grep -v '^#!' | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown flag: $1" ;;
  esac
done

[[ -n "$HOST" ]] || die "--host <IP> is required"
[[ -n "$SSH_KEY" ]] || die "--ssh-key <path> is required"
[[ -f "$SSH_KEY" ]] || die "ssh key not found: $SSH_KEY"
vm_scheme "$TLS_MODE" >/dev/null || die "--tls must be letsencrypt or http"
# External gateways register on :443 (the console's K8s command hardcodes the port),
# so they only line up under letsencrypt. Warn instead of failing.
if [[ "$EXTERNAL_GATEWAYS" == "true" && "$TLS_MODE" == "http" ]]; then
  log "WARNING: --tls http exposes the control plane but external gateways expect :443 (letsencrypt). Use --tls letsencrypt to connect external gateways, or --no-external-gateways to drop the cp endpoint."
fi

SSH=(ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "${SSH_USER}@${HOST}")
REMOTE_DIR="/opt/amp-quickstart"
SCHEME="$(vm_scheme "$TLS_MODE")"

remote() { "${SSH[@]}" "$@"; }
remote_run() {
  # Runs remote-install.sh with the install config in the remote env.
  local phase="$1"; shift || true
  remote "sudo VM_IP='${HOST}' TLS_MODE='${TLS_MODE}' EXTERNAL_GATEWAYS='${EXTERNAL_GATEWAYS}' \
    ACME_EMAIL='${ACME_EMAIL}' VERSION='${VERSION:-0.0.0-dev}' \
    bash \"${REMOTE_DIR}/vm/remote-install.sh\" \"${phase}\" \"$*\""
}

log "Copying installer to ${SSH_USER}@${HOST}:${REMOTE_DIR}"
remote "sudo mkdir -p '${REMOTE_DIR}' && sudo chown -R '${SSH_USER}' '${REMOTE_DIR}'"
rsync -az -e "ssh -i '${SSH_KEY}' -o StrictHostKeyChecking=accept-new" \
  "${QS_DIR}/" "${SSH_USER}@${HOST}:${REMOTE_DIR}/"

log "Phase 1/3: bootstrap (Docker + firewall)"
remote_run bootstrap

if [[ "$TLS_MODE" == "letsencrypt" ]]; then
  log "Phase 2/3: preflight — verifying :80/:443 reachable from the internet"
  for port in 80 443; do
    remote_run preflight "$port" &
    sleep 3
    if ! nc -z -w 5 "$HOST" "$port"; then
      wait || true
      die "Port ${port} is NOT reachable from this machine.
  Open inbound ${port}/tcp in your cloud security group / NACL,
  or rerun with --tls http to skip Let's Encrypt."
    fi
    wait || true
    log "  :${port} reachable"
  done
fi

log "Phase 3/3: install Agent Manager + start Caddy (this takes 8-15 min)"
remote_run install

log "Done. Access URLs:"
cat <<EOF

  Console:   ${SCHEME}://$(vm_host console "$HOST")
  API:       ${SCHEME}://$(vm_host api "$HOST")
  Thunder:   ${SCHEME}://$(vm_host thunder "$HOST")
  Observer:  ${SCHEME}://$(vm_host observer "$HOST")
  OTel + agent endpoints: ${SCHEME}://$(vm_host gateway "$HOST")/<context>
EOF
[[ "$EXTERNAL_GATEWAYS" == "true" ]] && echo "  Gateway control plane: ${SCHEME}://$(vm_host cp "$HOST")  (connect external gateways here; registration token is secret-bearing)"
