#!/usr/bin/env bash
# Render assertions for wso2-amp-evaluation-extension.
# The NetworkPolicy is matched post-DNAT: a rule naming a Service's address or port matches nothing.
set -uo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAILURES=0

# apiserver_rule [helm --set args...] -> "<cidr,...>|<port,...>" for the egress
# rule carrying ipBlocks, or "" when no such rule renders.
apiserver_rule() {
  local rendered
  if ! rendered="$(helm template test-release "$CHART_DIR" "$@" 2>&1)"; then
    printf 'helm template failed: %s\n' "$rendered" >&2
    return 1
  fi
  printf '%s' "$rendered" | python3 -c '
import sys, yaml
for doc in yaml.safe_load_all(sys.stdin):
    if not doc or doc.get("kind") != "NetworkPolicy":
        continue
    if doc["metadata"]["name"] != "amp-evaluation-job-egress":
        continue
    for rule in doc["spec"].get("egress", []) or []:
        tos = rule.get("to") or []
        ports = [str(p["port"]) for p in rule.get("ports") or []]
        # A rule with ports but no destinations allows those ports everywhere.
        # Report it rather than skipping, so it can never read as "no rule".
        if not tos and ports:
            print("ANY|" + ",".join(ports))
            continue
        blocks = [t["ipBlock"]["cidr"] for t in tos if "ipBlock" in t]
        # The dev-egress rule is also an ipBlock; the API-server rule is the one
        # that does not carry the dev-egress port.
        if blocks and "8080" not in ports:
            print(",".join(blocks) + "|" + ",".join(ports))
'
}

assert_rule() {
  local label="$1" expected="$2"
  shift 2
  local actual
  # Without this, a render failure returns empty and reads as a passing "no rule" case.
  if ! actual="$(apiserver_rule "$@")"; then
    printf 'FAIL - %s\n      render failed\n' "$label"
    FAILURES=$((FAILURES + 1))
    return
  fi
  if [[ "$expected" == "$actual" ]]; then
    printf 'ok   - %s\n' "$label"
  else
    printf 'FAIL - %s\n      expected: %q\n      actual:   %q\n' "$label" "$expected" "$actual"
    FAILURES=$((FAILURES + 1))
  fi
}

# The defect this guards: a default render that silently omits the rule.
assert_rule "default render carries the API-server rule on both control-plane ports" \
  "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16|443,6443"

# Helm must replace the default list, not merge into it — a merge would silently re-admit
# the pod and service CIDRs, so exactly one ipBlock has to survive.
assert_rule "an explicit cidrs[0] replaces the permissive default outright" \
  "172.19.0.0/16|443,6443" \
  --set "networkPolicy.evaluationJob.apiServer.cidrs[0]=172.19.0.0/16"

# An empty CIDR must drop the entry rather than render `cidr: null`.
assert_rule "an empty cidr entry renders no ipBlock" \
  "" \
  --set "networkPolicy.evaluationJob.apiServer.cidrs[0]="

# Changing a number here means checking the target's containerPort, not its Service port.
ns_rule_ports() {
  helm template test-release "$CHART_DIR" 2>/dev/null | python3 -c '
import sys, yaml
for doc in yaml.safe_load_all(sys.stdin):
    if not doc or doc.get("kind") != "NetworkPolicy":
        continue
    if doc["metadata"]["name"] != "amp-evaluation-job-egress":
        continue
    for rule in doc["spec"].get("egress", []) or []:
        names = []
        for t in rule.get("to") or []:
            ns = (t.get("namespaceSelector") or {}).get("matchLabels") or {}
            names += list(ns.values())
        if not names:
            continue
        ports = ",".join(str(p["port"]) for p in rule.get("ports") or [])
        print("+".join(names) + "=" + (ports if ports else "ALL"))
'
}

expected_ns_ports="kube-system=53,53
openchoreo-observability-plane=9098
amp-thunder=8090
wso2-amp=8080
true=22893"

actual_ns_ports="$(ns_rule_ports)"
if [[ "$expected_ns_ports" == "$actual_ns_ports" ]]; then
  printf 'ok   - every namespaceSelector rule targets its containerPort\n'
else
  printf 'FAIL - every namespaceSelector rule targets its containerPort\n      expected:\n%s\n      actual:\n%s\n' \
    "$expected_ns_ports" "$actual_ns_ports"
  FAILURES=$((FAILURES + 1))
fi

if [[ $FAILURES -gt 0 ]]; then
  printf '\nwso2-amp-evaluation-extension: %d render assertion(s) failed\n' "$FAILURES"
  exit 1
fi
printf '\nwso2-amp-evaluation-extension: render assertions passed\n'
