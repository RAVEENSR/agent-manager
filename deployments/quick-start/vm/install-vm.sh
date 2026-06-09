#!/usr/bin/env bash
# install-vm.sh — laptop-side orchestrator for "Agent Manager on a VM with Docker".
# Usage:
#   ./install-vm.sh --host <IP> --ssh-key <path> --version <amp-release>
#                   [--email <addr>] [--tls letsencrypt|http] [--ssh-user <user>]
#                   [--no-external-gateways] [--no-port80]
#
# --version: the amp/v* release to install (e.g. 0.15.0). Required — the charts
#   and manifests are pulled per-release; there is no sensible default.
#
# --no-port80: issue Let's Encrypt certs via the TLS-ALPN-01 challenge over :443
#   only (no inbound port 80 required). Requires --tls letsencrypt.
set -euo pipefail

HOST="" SSH_KEY="" SSH_USER="root" TLS_MODE="letsencrypt" ACME_EMAIL="" EXTERNAL_GATEWAYS="true" NO_PORT80="false"
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
    --tls) require_value "$1" "${2:-}"; TLS_MODE="$2"; shift 2 ;;
    --version) require_value "$1" "${2:-}"; AMP_VERSION="$2"; shift 2 ;;
    --no-external-gateways) EXTERNAL_GATEWAYS="false"; shift ;;
    --no-port80) NO_PORT80="true"; shift ;;
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
vm_scheme "$TLS_MODE" >/dev/null || die "--tls must be letsencrypt or http"
# --no-port80 only applies to Let's Encrypt (http mode serves on :80 by definition).
[[ "$NO_PORT80" == "true" && "$TLS_MODE" != "letsencrypt" ]] && \
  die "--no-port80 requires --tls letsencrypt (http mode serves on port 80)"
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
    NO_PORT80='${NO_PORT80}' ACME_EMAIL='${ACME_EMAIL}' VERSION='${AMP_VERSION}' \
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

if [[ "$TLS_MODE" == "letsencrypt" ]]; then
  # In --no-port80 mode only :443 must be reachable (TLS-ALPN-01); otherwise :80 too.
  preflight_ports=(80 443)
  [[ "$NO_PORT80" == "true" ]] && preflight_ports=(443)
  log "Phase 2/3: preflight — verifying ${preflight_ports[*]} reachable from the internet"
  for port in "${preflight_ports[@]}"; do
    remote_run preflight "$port" &
    # Retry within the remote listener's window instead of a single check after a
    # fixed sleep — a slow VM/SSH path may not have bound the socket yet.
    reachable="false"
    for _ in $(seq 1 12); do
      if nc -z -w 1 "$HOST" "$port"; then reachable="true"; break; fi
      sleep 1
    done
    if [[ "$reachable" != "true" ]]; then
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
