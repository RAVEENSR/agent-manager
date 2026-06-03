#!/usr/bin/env bash
# remote-install.sh — executes on the target VM. Invoked by install-vm.sh over SSH.
# Usage: <phase> where phase is one of: bootstrap | preflight | install
# Config via env: VM_IP, TLS_MODE(letsencrypt|http), EXTERNAL_GATEWAYS(true|false),
#                 ACME_EMAIL, VERSION.
set -euo pipefail

PHASE="${1:?usage: remote-install.sh <bootstrap|preflight|install>}"
VM_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QS_DIR="$(cd "${VM_DIR}/.." && pwd)"
# shellcheck source=lib-vm.sh
source "${VM_DIR}/lib-vm.sh"

: "${VM_IP:?VM_IP is required}"
: "${TLS_MODE:=letsencrypt}"
: "${EXTERNAL_GATEWAYS:=true}"
: "${ACME_EMAIL:=}"

log() { printf '\033[0;34m[vm:%s]\033[0m %s\n' "$PHASE" "$*"; }

ensure_docker() {
  if command -v docker >/dev/null 2>&1; then log "Docker present"; return; fi
  log "Installing Docker via get.docker.com"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker
}

ensure_firewall() {
  # Open only 80/443 publicly; k3d ports are loopback-bound. SSH stays as-is.
  if command -v ufw >/dev/null 2>&1; then
    ufw allow 80/tcp || true
    ufw allow 443/tcp || true
    log "ufw: opened 80,443"
  elif command -v firewall-cmd >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port=80/tcp || true
    firewall-cmd --permanent --add-port=443/tcp || true
    firewall-cmd --reload || true
    log "firewalld: opened 80,443"
  else
    log "No ufw/firewalld found; assuming host firewall is open for 80,443"
  fi
}

phase_bootstrap() {
  ensure_docker
  ensure_firewall
  log "Bootstrap complete"
}

# Opens a temporary listener on the given port so the laptop can verify the
# cloud security group permits inbound. Blocks for 8s; the caller
# (install-vm.sh) runs this in the background over SSH while it probes.
phase_preflight() {
  local port="${1:?preflight needs a port}"
  log "Opening temporary listener on :${port} for 8s"
  timeout 8 python3 - "$port" <<'PY' || true
import socket, sys, time
p = int(sys.argv[1])
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", p)); s.listen(1)
time.sleep(8)
PY
}

phase_install() {
  local scheme; scheme="$(vm_scheme "$TLS_MODE")"

  # Build the override arrays install.sh honors.
  # shellcheck disable=SC2034  # arrays are inherited by the subshell that sources install.sh
  mapfile -t AMP_HELM_ARGS < <(build_amp_helm_args "$VM_IP" "$scheme" "$EXTERNAL_GATEWAYS")
  # shellcheck disable=SC2034
  mapfile -t THUNDER_HELM_ARGS < <(build_thunder_helm_args "$VM_IP" "$scheme")
  # shellcheck disable=SC2034
  mapfile -t GATEWAY_HELM_ARGS < <(build_gateway_helm_args "$VM_IP" "$scheme")
  export VERSION="${VERSION:-0.0.0-dev}"

  # Loopback-bound k3d config.
  render_k3d_vm_config <"${QS_DIR}/k3d-config.yaml" >/tmp/k3d-config-vm.yaml
  export K3D_CONFIG=/tmp/k3d-config-vm.yaml

  log "Running base installer with sslip.io overrides (${scheme})"
  # Subshell: install.sh's exit calls stay contained; arrays are inherited.
  # The `|| rc=$?` keeps the subshell out of set -e so we capture its status
  # instead of aborting before the check below.
  local rc=0
  ( set +e; source "${QS_DIR}/install.sh" ) || rc=$?
  if [[ "$rc" -ne 0 ]]; then log "Base installer exited $rc"; return "$rc"; fi

  start_caddy "$scheme"
}

start_caddy() {
  local scheme="$1"
  mkdir -p /opt/amp
  render_caddyfile "$VM_IP" "$scheme" "$ACME_EMAIL" "$EXTERNAL_GATEWAYS" >/opt/amp/Caddyfile
  log "Wrote /opt/amp/Caddyfile"

  docker rm -f amp-caddy >/dev/null 2>&1 || true
  docker run -d --name amp-caddy --restart unless-stopped \
    --network host \
    -v amp-caddy-data:/data \
    -v amp-caddy-config:/config \
    -v /opt/amp/Caddyfile:/etc/caddy/Caddyfile:ro \
    caddy:2
  log "Caddy started on :80/:443"
}

case "$PHASE" in
  bootstrap) phase_bootstrap ;;
  preflight) phase_preflight "${2:?preflight needs a port}" ;;
  install)   phase_install ;;
  *) echo "unknown phase: $PHASE" >&2; exit 2 ;;
esac
