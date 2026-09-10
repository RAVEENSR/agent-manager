#!/bin/bash
set -euo pipefail

# install-kata.sh — Install the Kata Containers isolation tier on a Linux Kubernetes cluster.
#
# Unlike gVisor (a single userspace `runsc` binary — see install-gvisor.sh), Kata boots each
# agent pod in a lightweight VM with its own guest kernel. The full stack (hypervisor + guest
# kernel + initrd + virtiofsd) is installed by the upstream **kata-deploy** DaemonSet, applied
# from any machine with kubectl access — you do NOT run this per-node like install-gvisor.sh.
#
# This mirrors the upstream agent-sandbox GKE example:
#   https://agent-sandbox.sigs.k8s.io/docs/use-cases/kata-containers-isolation/
#
# What it does (no downtime for existing runc agents):
#   1. Pre-flight: nested virtualization (/dev/kvm) must be available on the target node(s).
#   2. Label the target node(s) `kata=true` so the install + Kata pods land ONLY there.
#   3. Apply kata-rbac + the kata-deploy DaemonSet, SCOPED to the labeled node(s) so the
#      server node and running runc agents are never reconfigured.
#   4. Wait for the kata-deploy rollout, then register the `kata-qemu` RuntimeClass
#      (with scheduling) and taint the node so only Kata pods schedule onto it.
#
# Usage (from a machine with kubectl pointed at the cluster):
#   KATA_NODES="node-a node-b" bash install-kata.sh
#   # or label the node(s) yourself first and run with no args (installs on every node
#   # already labeled kata=true).
#
# Requirements for each Kata node:
#   - Nested virtualization enabled (Intel N2 / bare metal with KVM; NOT E2/AMD-N2D/COS on GKE)
#   - Ubuntu/containerd (Container-Optimized OS is read-only and blocks the installer)
#   - x86_64 (Intel) strongly recommended; review upstream restrictions for arm64
#
# Idempotent: safe to re-run. Already-applied resources are updated in place.

echo "=== Installing Kata Containers (kata-qemu) isolation tier ==="

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Source shared vars when run from inside the repo; fall back to sane defaults when piped.
if [ -f "$SCRIPT_DIR/env.sh" ]; then source "$SCRIPT_DIR/env.sh"; fi
KATA_VERSION="${KATA_VERSION:-3.2.0}"
KATA_RUNTIME_CLASS="${KATA_RUNTIME_CLASS:-kata-qemu}"
KATA_NODE_LABEL_KEY="${KATA_NODE_LABEL_KEY:-kata}"
KATA_NODE_LABEL_VALUE="${KATA_NODE_LABEL_VALUE:-true}"
KATA_NODES="${KATA_NODES:-}"   # space-separated node names; empty = use already-labeled nodes

if ! command -v kubectl &>/dev/null; then
    echo "❌ kubectl not found. Run this from a machine with kubectl access to the cluster."
    exit 1
fi

# --- 1. Label the target node(s) so kata-deploy + Kata pods land only there ---
if [ -n "$KATA_NODES" ]; then
    for n in $KATA_NODES; do
        echo "🏷️  Labeling node ${n} ${KATA_NODE_LABEL_KEY}=${KATA_NODE_LABEL_VALUE}..."
        kubectl label node "$n" "${KATA_NODE_LABEL_KEY}=${KATA_NODE_LABEL_VALUE}" --overwrite
    done
fi

LABELED=$(kubectl get nodes -l "${KATA_NODE_LABEL_KEY}=${KATA_NODE_LABEL_VALUE}" -o name 2>/dev/null || true)
if [ -z "$LABELED" ]; then
    echo "❌ No nodes are labeled ${KATA_NODE_LABEL_KEY}=${KATA_NODE_LABEL_VALUE}."
    echo "   Pass KATA_NODES=\"<node> ...\" or label the Kata node(s) first:"
    echo "     kubectl label node <node> ${KATA_NODE_LABEL_KEY}=${KATA_NODE_LABEL_VALUE} --overwrite"
    exit 1
fi
echo "   Target Kata node(s):"
echo "$LABELED" | sed 's/^/     /'

# --- Pre-flight: nested virtualization (KVM) is MANDATORY on each target node ---
# Kata-qemu boots a real VM via QEMU/KVM, so a node without /dev/kvm cannot run it (pods
# would hang in ContainerCreating). We verify by running a tiny privileged pod ON each
# target node and checking for /dev/kvm. If ANY target node lacks it, we ABORT without
# installing — "Kata cannot install without KVM support". Set KATA_SKIP_KVM_CHECK=true to
# bypass only if you have already verified KVM out of band.
KATA_SKIP_KVM_CHECK="${KATA_SKIP_KVM_CHECK:-false}"
if [ "$KATA_SKIP_KVM_CHECK" = "true" ]; then
    echo "⏭️  KATA_SKIP_KVM_CHECK=true — skipping the /dev/kvm pre-flight (you asserted KVM is present)."
else
    echo "🔬 Verifying nested virtualization (/dev/kvm) on the target node(s)..."
    KVM_FAILED=false
    for node in $(echo "$LABELED" | sed 's#node/##'); do
        POD="kata-kvm-check-${node//[^a-z0-9-]/-}"
        kubectl delete pod "$POD" --ignore-not-found >/dev/null 2>&1 || true
        cat <<EOF | kubectl apply -f - >/dev/null 2>&1 || true
apiVersion: v1
kind: Pod
metadata:
  name: ${POD}
spec:
  nodeName: ${node}
  restartPolicy: Never
  tolerations:
    - operator: Exists
  containers:
    - name: check
      image: busybox:1.36
      securityContext: { privileged: true }
      command: ["sh","-c","test -e /dev/kvm && echo KVM_OK || echo KVM_MISSING"]
      volumeMounts: [{ name: dev, mountPath: /dev }]
  volumes:
    - name: dev
      hostPath: { path: /dev }
EOF
        if kubectl wait --for=jsonpath='{.status.phase}'=Succeeded "pod/${POD}" --timeout=60s >/dev/null 2>&1 \
            && kubectl logs "$POD" 2>/dev/null | grep -q KVM_OK; then
            echo "   ✅ ${node}: /dev/kvm present"
        else
            echo "   ❌ ${node}: /dev/kvm NOT available (or check could not complete)."
            KVM_FAILED=true
        fi
        kubectl delete pod "$POD" --ignore-not-found >/dev/null 2>&1 || true
    done

    if [ "$KVM_FAILED" = "true" ]; then
        echo ""
        echo "❌ Kata cannot be installed: one or more target nodes have no KVM / nested-virtualization support."
        echo "   Kata-qemu requires hardware virtualization (VT-x/AMD-V) exposed as /dev/kvm. Use:"
        echo "     - a bare-metal node (e.g. an EKS *.metal instance), or"
        echo "     - a nested-virt cloud VM (GCP Intel N2 + --enable-nested-virtualization, Ubuntu/containerd)."
        echo "   Verify on the node:  ls -l /dev/kvm   and   cat /sys/module/kvm_intel/parameters/nested  (expect Y)."
        echo "   Already verified KVM yourself? Re-run with KATA_SKIP_KVM_CHECK=true."
        exit 1
    fi
fi

# --- 2. Apply kata-deploy RBAC + DaemonSet, scoped to the labeled node(s) ---
# We inject our nodeSelector BEFORE applying so kata-deploy NEVER lands on the server
# node (where reconfiguring containerd would disrupt running runc agents).
KATA_RAW="https://raw.githubusercontent.com/kata-containers/kata-containers/${KATA_VERSION}/tools/packaging/kata-deploy"
echo "📥 Applying kata-deploy RBAC (kata v${KATA_VERSION})..."
kubectl apply -f "${KATA_RAW}/kata-rbac/base/kata-rbac.yaml"

echo "📥 Applying kata-deploy DaemonSet (scoped to ${KATA_NODE_LABEL_KEY}=${KATA_NODE_LABEL_VALUE})..."
TMP_DS="$(mktemp)"
curl -fsSL "${KATA_RAW}/kata-deploy/base/kata-deploy.yaml" -o "$TMP_DS"
# The upstream DaemonSet ships with NO nodeSelector and NO tolerations, so by default it
# would run on EVERY node and reconfigure each node's containerd — disrupting the server
# node's running runc agents. We inject (BEFORE applying):
#   - nodeSelector kata=true  → installs ONLY on the labeled Kata node(s)
#   - a toleration for the kata taint → so re-runs still schedule after the node is tainted
# We anchor on the pod-spec line `serviceAccountName: kata-deploy-sa` (6-space indent). If it
# isn't found (manifest changed shape), we ABORT rather than apply an unscoped DaemonSet.
if ! grep -q "serviceAccountName: kata-deploy-sa" "$TMP_DS"; then
    echo "❌ kata-deploy.yaml shape changed (no 'serviceAccountName: kata-deploy-sa' anchor)."
    echo "   Refusing to apply an unscoped DaemonSet (it would touch the server node)."
    echo "   Inspect ${TMP_DS} and scope its nodeSelector to ${KATA_NODE_LABEL_KEY}=${KATA_NODE_LABEL_VALUE} manually."
    exit 1
fi
awk -v k="$KATA_NODE_LABEL_KEY" -v v="$KATA_NODE_LABEL_VALUE" '
    { print }
    /serviceAccountName: kata-deploy-sa/ {
        print "      nodeSelector:"
        print "        " k ": \"" v "\""
        print "      tolerations:"
        print "        - key: \"" k "\""
        print "          operator: \"Equal\""
        print "          value: \"" v "\""
        print "          effect: \"NoSchedule\""
    }
' "$TMP_DS" > "${TMP_DS}.scoped"
mv "${TMP_DS}.scoped" "$TMP_DS"
kubectl apply -f "$TMP_DS"
rm -f "$TMP_DS"

echo "⏳ Waiting for kata-deploy rollout (installs the Kata stack on the node)..."
kubectl -n kube-system rollout status daemonset/kata-deploy --timeout=10m

# --- 2b. k3s nodes: kata-deploy installs the Kata stack into /opt/kata but does NOT wire
# k3s's containerd for it. Upstream kata-deploy only recognizes a handful of distro markers
# (k3d, RKE2, ...) and otherwise falls back to writing /etc/containerd/config.toml +
# `systemctl restart containerd` — neither of which k3s uses (k3s bundles its own containerd,
# configured only via .../agent/etc/containerd/config.toml.tmpl, read only at containerd
# startup). Left unfixed, pods get "no runtime for kata-qemu is configured" forever even
# though kata-deploy itself reports success. Detect k3s via containerRuntimeVersion (e.g.
# "containerd://2.1.4-k3s1.32") and patch directly via a privileged pod that nsenters the
# host's namespaces — this works whether install-kata.sh runs on the node itself or from a
# separate machine with only kubectl access (no SSH assumed).
K3S_FIX_SCRIPT=$(cat <<'FIXEOF'
set -e
mkdir -p /usr/local/bin
ln -sf /opt/kata/bin/containerd-shim-kata-v2 /usr/local/bin/containerd-shim-kata-qemu-v2
TMPL=/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl
mkdir -p "$(dirname "$TMPL")"
if grep -q "runtimes.kata-qemu" "$TMPL" 2>/dev/null; then
    echo UNCHANGED
else
    cat > "$TMPL" <<'INNEREOF'
{{ template "base" . }}

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata-qemu]
  runtime_type = "io.containerd.kata-qemu.v2"
  privileged_without_host_devices = true
  pod_annotations = ["io.katacontainers.*"]
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata-qemu.options]
    ConfigPath = "/opt/kata/share/defaults/kata-containers/configuration-qemu.toml"
INNEREOF
    # k3s only reads config.toml.tmpl at containerd startup — force a reload. Try both unit
    # names since either can be present depending on whether this node is a server or agent.
    systemctl restart k3s-agent 2>/dev/null || systemctl restart k3s 2>/dev/null || true
    echo CHANGED
fi
FIXEOF
)
FIX_SCRIPT_B64=$(printf '%s' "$K3S_FIX_SCRIPT" | base64 | tr -d '\n')

echo "🔍 Checking target node(s) for k3s (kata-deploy doesn't auto-wire k3s's containerd)..."
for node in $(echo "$LABELED" | sed 's#node/##'); do
    RUNTIME=$(kubectl get node "$node" -o jsonpath='{.status.nodeInfo.containerRuntimeVersion}' 2>/dev/null || true)
    case "$RUNTIME" in
        *-k3s*)
            echo "   🔧 ${node}: k3s detected (${RUNTIME}) — patching containerd directly"
            FIXPOD="kata-k3s-fix-${node//[^a-zA-Z0-9-]/-}"
            kubectl delete pod "$FIXPOD" --ignore-not-found >/dev/null 2>&1 || true
            cat <<PODEOF | kubectl apply -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: ${FIXPOD}
spec:
  nodeName: ${node}
  hostPID: true
  restartPolicy: Never
  tolerations:
    - operator: Exists
  volumes:
    - name: host-tmp
      hostPath: { path: /tmp }
  containers:
    - name: fix
      image: busybox:1.36
      securityContext:
        privileged: true
      env:
        - name: FIX_SCRIPT_B64
          value: "${FIX_SCRIPT_B64}"
      volumeMounts:
        - name: host-tmp
          mountPath: /host-tmp
      command:
        - sh
        - -c
        - |
          echo "\$FIX_SCRIPT_B64" | base64 -d > /host-tmp/kata-k3s-fix.sh
          chmod +x /host-tmp/kata-k3s-fix.sh
          nsenter -t 1 -m -u -i -n -p -- sh /tmp/kata-k3s-fix.sh
          rm -f /host-tmp/kata-k3s-fix.sh
PODEOF
            if ! kubectl wait --for=jsonpath='{.status.phase}'=Succeeded "pod/${FIXPOD}" --timeout=60s >/dev/null 2>&1; then
                echo "   ❌ ${node}: containerd-wiring pod did not complete — inspect it before continuing:"
                echo "        kubectl logs ${FIXPOD}"
                kubectl logs "$FIXPOD" 2>/dev/null || true
                kubectl delete pod "$FIXPOD" --ignore-not-found >/dev/null 2>&1 || true
                exit 1
            fi
            RESULT=$(kubectl logs "$FIXPOD" 2>/dev/null | tail -1)
            kubectl delete pod "$FIXPOD" --ignore-not-found >/dev/null 2>&1 || true
            if [ "$RESULT" = "CHANGED" ]; then
                echo "   🔄 ${node}: containerd config changed — waiting for the node to rejoin Ready..."
                kubectl wait --for=condition=Ready "node/${node}" --timeout=180s
            else
                echo "   ✅ ${node}: containerd kata-qemu runtime already wired"
            fi
            ;;
        *)
            echo "   ✅ ${node}: not k3s (${RUNTIME:-unknown}) — kata-deploy's systemd/containerd install applies"
            ;;
    esac
done

# --- 3. Register the RuntimeClass (with scheduling) and taint the node ---
echo "🧩 Registering the '${KATA_RUNTIME_CLASS}' RuntimeClass..."
if [ -f "$SCRIPT_DIR/../k8s/kata-runtimeclass.yaml" ]; then
    kubectl apply -f "$SCRIPT_DIR/../k8s/kata-runtimeclass.yaml"
else
    # Release-pinned, not main: a versioned install applying a manifest from the
    # tip of main is how a cluster ends up running something the rest of the
    # install has never been tested against. AMP_MANIFEST_BASE_URL lets a fork or
    # an air-gapped mirror repoint this without editing the script.
    kubectl apply -f "${AMP_MANIFEST_BASE_URL:-https://raw.githubusercontent.com/wso2/agent-manager/${AMP_RELEASE_REF:-main}/deployments/k8s}/kata-runtimeclass.yaml"
fi

echo "🏷️  Tainting the Kata node(s) so only Kata pods schedule there..."
for node in $(echo "$LABELED" | sed 's#node/##'); do
    kubectl taint node "$node" "${KATA_NODE_LABEL_KEY}=${KATA_NODE_LABEL_VALUE}:NoSchedule" --overwrite
done

# --- 4. Ensure Fluent Bit (log DaemonSet) tolerates the Kata taint ---
if kubectl get daemonset fluent-bit -n openchoreo-observability-plane &>/dev/null; then
    echo "🪵 Ensuring Fluent Bit tolerates the Kata taint (so agent logs are collected)..."
    kubectl patch daemonset fluent-bit -n openchoreo-observability-plane --type=json \
        -p='[{"op":"add","path":"/spec/template/spec/tolerations","value":[{"operator":"Exists"}]}]' >/dev/null 2>&1 || true
fi

echo ""
echo "✅ Kata isolation tier installed."
echo ""
echo "Verify:"
echo "  kubectl get runtimeclass ${KATA_RUNTIME_CLASS}"
echo "  kubectl -n kube-system get pods -l name=kata-deploy -o wide"
echo ""
echo "Smoke test (should boot a VM with a different kernel than the host):"
echo "  kubectl run kata-test --image=busybox:1.36 --restart=Never \\"
echo "    --overrides='{\"spec\":{\"runtimeClassName\":\"${KATA_RUNTIME_CLASS}\"}}' -- uname -r"
echo "  kubectl logs kata-test    # kernel version differs from 'uname -r' on the host node"
echo "  kubectl delete pod kata-test"
echo ""
echo "Then create a Kata environment and deploy/promote an agent to it:"
echo "  ISOLATION_TIER=kata ENV_NAME=kata-dev DISPLAY_NAME=\"Kata Dev\" \\"
echo "    AGENT_MANAGER_TOKEN=<token> bash deployments/scripts/add-environment.sh"
