#!/usr/bin/env bash
# build-vm-bundle.sh — package the combined install bundle attached to a release.
#
# The bundle is a single tarball with everything install-vm.sh / install-advanced.sh /
# start.sh read at runtime, so installers need ONE download instead of a git clone
# plus many raw.githubusercontent fetches (which GitHub rate-limits per IP). It
# reproduces the repo layout so install.sh resolves DEPLOYMENTS_DIR (=deployments/)
# and its single-cluster/values/k8s/scripts siblings exactly as in a checkout.
#
# Also copies bootstrap.sh and start.sh out alongside the tarball so each can be
# attached as its own release asset (the stable curl entry points for the VM and
# quick-start install paths respectively — piping a tarball to bash doesn't work,
# so these stay standalone files even though a copy also lives inside the bundle).
#
# Usage: build-vm-bundle.sh <version> <out-dir>   (run from the repo root)
set -euo pipefail

VERSION="${1:?usage: build-vm-bundle.sh <version> <out-dir>}"
OUT_DIR="${2:?usage: build-vm-bundle.sh <version> <out-dir>}"
mkdir -p "$OUT_DIR"

BUNDLE="${OUT_DIR}/wso2-agent-manager-${VERSION}.tar.gz"

# Directories the installers read at runtime (see install.sh DEPLOYMENTS_DIR +
# install-helpers.sh ../scripts). Charts are pulled from OCI registries at install
# time, so helm-charts/ is intentionally excluded.
tar -czf "$BUNDLE" \
  deployments/vm \
  deployments/quick-start \
  deployments/single-cluster \
  deployments/values \
  deployments/k8s \
  deployments/scripts

cp deployments/vm/bootstrap.sh "${OUT_DIR}/bootstrap.sh"
cp deployments/quick-start/start.sh "${OUT_DIR}/start.sh"

echo "✅ Built install bundle: $BUNDLE"
echo "   Contents (top level):"
# head closes the pipe after 20 lines; under `set -o pipefail` the SIGPIPE tar/sed
# then take would fail the build, so cap first and swallow the expected broken pipe.
tar -tzf "$BUNDLE" | head -n 20 | sed 's/^/     /' || true
echo "✅ Copied bootstrap.sh: ${OUT_DIR}/bootstrap.sh"
echo "✅ Copied start.sh: ${OUT_DIR}/start.sh"
