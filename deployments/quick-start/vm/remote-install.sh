#!/usr/bin/env bash
# remote-install.sh — executes on the target VM. Invoked by install-vm.sh over SSH.
# Usage: <phase> where phase is one of: bootstrap | preflight | install
# Config via env: VM_IP, TLS_MODE(letsencrypt|http), EXTERNAL_GATEWAYS(true|false),
#                 NO_PORT80(true|false), ACME_EMAIL, VERSION.
set -euo pipefail

PHASE="${1:?usage: remote-install.sh <bootstrap|preflight|install>}"
VM_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QS_DIR="$(cd "${VM_DIR}/.." && pwd)"
# shellcheck source=lib-vm.sh
source "${VM_DIR}/lib-vm.sh"

: "${VM_IP:?VM_IP is required}"
: "${TLS_MODE:=letsencrypt}"
: "${EXTERNAL_GATEWAYS:=true}"
: "${NO_PORT80:=false}"
: "${ACME_EMAIL:=}"

log() { printf '\033[0;34m[vm:%s]\033[0m %s\n' "$PHASE" "$*"; }
die() { printf '\033[0;31m[vm:%s] ERROR:\033[0m %s\n' "$PHASE" "$*" >&2; exit 1; }

ensure_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    log "Installing Docker via get.docker.com"
    curl -fsSL https://get.docker.com | sh
  else
    log "Docker CLI present"
  fi
  # command -v docker does not imply the daemon is running; bring it up either way.
  systemctl enable --now docker
  # Wait for the daemon to answer before anything else uses it.
  local _
  for _ in $(seq 1 15); do docker info >/dev/null 2>&1 && return; sleep 2; done
  die "Docker daemon did not become ready"
}

# install.sh only *verifies* k3d/kubectl/helm/lsof (it targets a pre-provisioned
# dev container). On a bare VM we must install them. Each step is idempotent.
ensure_prerequisites() {
  local arch; arch="$(dpkg --print-architecture)"   # amd64 | arm64

  # Tools later phases assume exist on a minimal image: curl (downloads),
  # python3 (preflight listener), lsof (install.sh port check).
  local pkgs=()
  command -v curl    >/dev/null 2>&1 || pkgs+=(curl)
  command -v python3 >/dev/null 2>&1 || pkgs+=(python3)
  command -v lsof    >/dev/null 2>&1 || pkgs+=(lsof)
  if (( ${#pkgs[@]} )); then
    log "Installing base packages: ${pkgs[*]}"
    apt-get update -qq && apt-get install -y -qq "${pkgs[@]}"
  fi

  ensure_docker

  if ! command -v k3d >/dev/null 2>&1; then
    log "Installing k3d"
    curl -fsSL https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash
  fi
  if ! command -v kubectl >/dev/null 2>&1; then
    log "Installing kubectl (${arch})"
    local kver; kver="$(curl -fsSL https://dl.k8s.io/release/stable.txt)"
    curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${kver}/bin/linux/${arch}/kubectl"
    chmod +x /usr/local/bin/kubectl
  fi
  if ! command -v helm >/dev/null 2>&1; then
    log "Installing helm"
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
  fi
}

ensure_firewall() {
  # Open only the ports the selected frontend mode needs (k3d ports are loopback-
  # bound; SSH stays as-is). http serves on :80; --no-port80 needs only :443;
  # default letsencrypt needs both (80 for HTTP-01 + redirect).
  local ports
  if [[ "$TLS_MODE" == "http" ]]; then
    ports=(80)
  elif [[ "$NO_PORT80" == "true" ]]; then
    ports=(443)
  else
    ports=(80 443)
  fi
  local port
  if command -v ufw >/dev/null 2>&1; then
    for port in "${ports[@]}"; do ufw allow "${port}/tcp" || true; done
    log "ufw: opened ${ports[*]}"
  elif command -v firewall-cmd >/dev/null 2>&1; then
    for port in "${ports[@]}"; do firewall-cmd --permanent --add-port="${port}/tcp" || true; done
    firewall-cmd --reload || true
    log "firewalld: opened ${ports[*]}"
  else
    log "No ufw/firewalld found; assuming host firewall is open for ${ports[*]}"
  fi
}

# Warn (don't block) when the root filesystem is too small to build agents. The
# in-cluster image store alone grows past 13 GB once agents are built; below ~40 GB
# free the node hits DiskPressure, which evicts pods and can take cluster DNS down
# mid-build. 50 GB is the documented minimum.
ensure_disk() {
  local avail_kb min_kb=$((40 * 1024 * 1024))
  avail_kb="$(df -Pk / | awk 'NR==2 {print $4}')"
  if [[ -n "$avail_kb" && "$avail_kb" -lt "$min_kb" ]]; then
    log "WARNING: only $((avail_kb / 1024 / 1024)) GB free on / — agent builds may"
    log "         hit DiskPressure. A 50 GB+ disk is recommended (see the VM docs)."
  fi
}

phase_bootstrap() {
  ensure_prerequisites
  ensure_firewall
  ensure_disk
  log "Bootstrap complete"
}

# Opens a temporary listener on the given port so the laptop can verify the
# cloud security group permits inbound. Blocks for 15s; the caller
# (install-vm.sh) runs this in the background over SSH while it retries probes.
phase_preflight() {
  local port="${1:?preflight needs a port}"
  log "Opening temporary listener on :${port} for 15s"
  local rc=0
  timeout 15 python3 - "$port" <<'PY' || rc=$?
import socket, sys, time
p = int(sys.argv[1])
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", p)); s.listen(1)
time.sleep(15)
PY
  # 0 = exited cleanly, 124 = timeout (both expected). Anything else (e.g. the
  # bind failed because the port is already in use) is a real error — surface it
  # so the laptop probe can't pass against some other listener.
  case "$rc" in
    0|124) ;;
    *) die "Could not open temporary listener on :${port} (already in use?)" ;;
  esac
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
  # shellcheck disable=SC2034
  mapfile -t CP_HELM_ARGS < <(build_cp_helm_args "$VM_IP" "$scheme")
  # shellcheck disable=SC2034
  mapfile -t PLATFORM_RESOURCES_HELM_ARGS < <(build_platform_resources_helm_args)
  # No safe default: install.sh builds chart refs + raw manifest URLs from amp/v${VERSION},
  # so a placeholder like 0.0.0-dev 404s. Require a real release.
  : "${VERSION:?VERSION is required (an existing amp/v* release, e.g. 0.15.0)}"
  export VERSION

  # Loopback-bound k3d config.
  render_k3d_vm_config <"${QS_DIR}/k3d-config.yaml" >/tmp/k3d-config-vm.yaml
  export K3D_CONFIG=/tmp/k3d-config-vm.yaml

  # CoreDNS rewrites pointed at the k3d server node (not host.k3d.internal), so
  # in-cluster name resolution still reaches the service ports after they are
  # loopback-bound. CLUSTER_NAME is fixed to "amp-local" in install.sh, so the
  # single server node is always "k3d-amp-local-server-0".
  render_coredns_vm_config "k3d-amp-local-server-0" >/tmp/coredns-amp-vm.yaml
  export COREDNS_FILE=/tmp/coredns-amp-vm.yaml

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
  render_caddyfile "$VM_IP" "$scheme" "$ACME_EMAIL" "$EXTERNAL_GATEWAYS" "$NO_PORT80" >/opt/amp/Caddyfile
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
