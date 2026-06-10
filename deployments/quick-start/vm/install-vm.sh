#!/usr/bin/env bash
# install-vm.sh — run ON the target VM (with sudo) to install Agent Manager.
# Usage:
#   sudo ./install-vm.sh --host <PUBLIC_IP> --version <amp-release> \
#                        [--email <addr>] [--no-external-gateways]
#
# --host: the VM's PUBLIC IPv4 address. Public URLs are derived as
#   *.amp.<IP>.sslip.io, and a cloud VM usually can't read its own public IP
#   (it is NAT'd), so pass it explicitly — you already know it (you used it to
#   SSH in).
# --version: the amp/v* release to install (e.g. 0.15.0). Required — the charts
#   and manifests are pulled per-release; there is no sensible default.
#
# TLS is always Let's Encrypt, 443-only: certificates issue via the TLS-ALPN-01
# challenge inside the :443 handshake, so only inbound 443 is required (no port
# 80). The public :443 must reach the VM as raw TCP (no TLS-terminating proxy in
# front). Docker, k3d, kubectl, helm and lsof are installed automatically if missing.
set -euo pipefail

VM_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QS_DIR="$(cd "${VM_DIR}/.." && pwd)"
# shellcheck source=lib-vm.sh
source "${VM_DIR}/lib-vm.sh"

VM_IP="" ACME_EMAIL="" EXTERNAL_GATEWAYS="true"
# Capture the amp release from --version or the VERSION env, but keep it out of
# the exported environment until the install step: get.docker.com (and other
# piped installers run during bootstrap) read $VERSION as the version to install
# and fail on the AMP release string.
AMP_VERSION="${VERSION:-}"
unset VERSION 2>/dev/null || true

log() { printf '\033[0;34m[install-vm]\033[0m %s\n' "$*"; }
die() { printf '\033[0;31m[install-vm] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }
require_value() { [[ -n "${2:-}" && "${2:-}" != --* ]] || die "$1 requires a value"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) require_value "$1" "${2:-}"; VM_IP="$2"; shift 2 ;;
    --version) require_value "$1" "${2:-}"; AMP_VERSION="$2"; shift 2 ;;
    --email) require_value "$1" "${2:-}"; ACME_EMAIL="$2"; shift 2 ;;
    --no-external-gateways) EXTERNAL_GATEWAYS="false"; shift ;;
    -h|--help) grep '^#' "$0" | grep -v '^#!' | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown flag: $1" ;;
  esac
done

[[ "$(id -u)" -eq 0 ]] || \
  die "run with sudo — this installs Docker, opens the firewall and creates the cluster: sudo $0 --host <IP> --version <release>"
[[ -n "$VM_IP" ]] || die "--host <PUBLIC_IP> is required (the VM's public IPv4 — sslip.io hostnames embed it)"
[[ "$VM_IP" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || \
  die "--host must be an IPv4 address (got '${VM_IP}')"
[[ -n "$AMP_VERSION" ]] || \
  die "--version <release> is required (an existing amp/v* tag, e.g. --version 0.15.0); see https://github.com/wso2/agent-manager/tags"

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

  # Tools the installer assumes exist on a minimal image: curl (downloads) and
  # lsof (install.sh port check).
  local pkgs=()
  command -v curl >/dev/null 2>&1 || pkgs+=(curl)
  command -v lsof >/dev/null 2>&1 || pkgs+=(lsof)
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
  # Only :443 faces the internet (k3d ports are loopback-bound; SSH stays as-is).
  # Certs issue via TLS-ALPN-01 inside the :443 handshake, so port 80 is never needed.
  if command -v ufw >/dev/null 2>&1; then
    ufw allow 443/tcp || true
    log "ufw: opened 443"
  elif command -v firewall-cmd >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port=443/tcp || true
    firewall-cmd --reload || true
    log "firewalld: opened 443"
  else
    log "No ufw/firewalld found; assuming the host firewall is open for 443"
  fi
  # The host firewall is only half the story — the cloud security group must also
  # permit inbound 443, and TLS-ALPN-01 needs it as raw TCP. We can't verify that
  # from inside the VM, so just remind; Caddy fails loudly if 443 is unreachable.
  log "Ensure inbound 443/tcp is open in your cloud security group (raw TCP, no TLS-terminating proxy) — Caddy needs it to obtain certificates."
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

start_caddy() {
  mkdir -p /opt/amp
  render_caddyfile "$VM_IP" "$ACME_EMAIL" "$EXTERNAL_GATEWAYS" >/opt/amp/Caddyfile
  log "Wrote /opt/amp/Caddyfile"

  docker rm -f amp-caddy >/dev/null 2>&1 || true
  docker run -d --name amp-caddy --restart unless-stopped \
    --network host \
    -v amp-caddy-data:/data \
    -v amp-caddy-config:/config \
    -v /opt/amp/Caddyfile:/etc/caddy/Caddyfile:ro \
    caddy:2
  log "Caddy started on :443"
}

run_install() {
  # Build the override arrays install.sh honors.
  # shellcheck disable=SC2034  # arrays are inherited by the subshell that sources install.sh
  mapfile -t AMP_HELM_ARGS < <(build_amp_helm_args "$VM_IP" "$EXTERNAL_GATEWAYS")
  # shellcheck disable=SC2034
  mapfile -t THUNDER_HELM_ARGS < <(build_thunder_helm_args "$VM_IP")
  # shellcheck disable=SC2034
  mapfile -t GATEWAY_HELM_ARGS < <(build_gateway_helm_args "$VM_IP")
  # shellcheck disable=SC2034
  mapfile -t CP_HELM_ARGS < <(build_cp_helm_args "$VM_IP")
  # shellcheck disable=SC2034
  mapfile -t PLATFORM_RESOURCES_HELM_ARGS < <(build_platform_resources_helm_args)
  # shellcheck disable=SC2034
  mapfile -t OBSERVABILITY_HELM_ARGS < <(build_observability_helm_args "$VM_IP")
  # Advertise deployed-agent endpoints under a public sslip.io host (Caddy fronts
  # the wildcard *.agents.<ip>.sslip.io with on-demand TLS), not the local default.
  DP_EXTERNAL_INGRESS="$(render_dataplane_external_ingress "$VM_IP")"
  export DP_EXTERNAL_INGRESS
  # install.sh builds chart refs + raw manifest URLs from amp/v${VERSION}; export it
  # only now, after bootstrap, so the piped installers above never saw it.
  export VERSION="$AMP_VERSION"

  # Suppress install.sh's localhost completion URLs — they are unreachable on a VM
  # (k3d ports are loopback-bound). This script prints the public sslip.io URLs below.
  export SHOW_LOCALHOST_URLS=false

  # Loopback-bound k3d config.
  render_k3d_vm_config <"${QS_DIR}/k3d-config.yaml" >/tmp/k3d-config-vm.yaml
  export K3D_CONFIG=/tmp/k3d-config-vm.yaml

  # CoreDNS rewrites pointed at the k3d server node (not host.k3d.internal), so
  # in-cluster name resolution still reaches the service ports after they are
  # loopback-bound. CLUSTER_NAME is fixed to "amp-local" in install.sh, so the
  # single server node is always "k3d-amp-local-server-0".
  render_coredns_vm_config "k3d-amp-local-server-0" >/tmp/coredns-amp-vm.yaml
  export COREDNS_FILE=/tmp/coredns-amp-vm.yaml

  log "Running base installer with sslip.io overrides (https)"
  # Subshell: install.sh's exit calls stay contained; arrays are inherited.
  # The `|| rc=$?` keeps the subshell out of set -e so we capture its status.
  local rc=0
  ( set +e; source "${QS_DIR}/install.sh" ) || rc=$?
  [[ "$rc" -eq 0 ]] || die "Base installer exited $rc"

  start_caddy
}

log "Phase 1/2: bootstrap (Docker + tools + firewall)"
ensure_prerequisites
ensure_firewall
ensure_disk

log "Phase 2/2: install Agent Manager + start Caddy (this takes 8-15 min)"
run_install

log "Done. Access URLs:"
cat <<EOF

  Console:   https://$(vm_host console "$VM_IP")
  API:       https://$(vm_host api "$VM_IP")
  Thunder:   https://$(vm_host thunder "$VM_IP")
  Observer:  https://$(vm_host observer "$VM_IP")
  OTel ingest: https://$(vm_host gateway "$VM_IP")/otel
  Deployed agents: https://<org>-<project>.agents.${VM_IP}.sslip.io/...
EOF
[[ "$EXTERNAL_GATEWAYS" == "true" ]] && echo "  Gateway control plane: https://$(vm_host cp "$VM_IP")  (connect external gateways here; registration token is secret-bearing)"
