#!/usr/bin/env bash
# Unit tests for .github/scripts/rewrite-image-registry.sh.
# Run: bash .github/scripts/tests/rewrite-image-registry.sh
#
# The fixtures deliberately include references the real charts do not have yet:
# a sibling product under the same org (api-platform/*) and an image whose name
# is a prefix of ours (amp-api-client). Those are the cases a prefix-matching
# rewrite gets wrong, and getting them wrong retags someone else's image into a
# registry that has never held it — which surfaces as an ImagePullBackOff on a
# customer's cluster, not here.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$REPO_ROOT/.github/scripts/rewrite-image-registry.sh"
FAILURES=0
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

assert_contains() {
    local label="$1" file="$2" needle="$3"
    if grep -qF -- "$needle" "$file"; then
        printf 'ok   - %s\n' "$label"
    else
        printf 'FAIL - %s\n      expected %q in %s\n' "$label" "$needle" "$(basename "$file")"
        FAILURES=$((FAILURES + 1))
    fi
}

assert_absent_re() {
    local label="$1" file="$2" regex="$3"
    if grep -qE -- "$regex" "$file"; then
        printf 'FAIL - %s\n      did not expect /%s/ in %s\n' "$label" "$regex" "$(basename "$file")"
        FAILURES=$((FAILURES + 1))
    else
        printf 'ok   - %s\n' "$label"
    fi
}

assert_absent() {
    local label="$1" file="$2" needle="$3"
    if grep -qF -- "$needle" "$file"; then
        printf 'FAIL - %s\n      did not expect %q in %s\n' "$label" "$needle" "$(basename "$file")"
        FAILURES=$((FAILURES + 1))
    else
        printf 'ok   - %s\n' "$label"
    fi
}

# A fixture tree the script can be pointed at, so the real charts are untouched.
setup() {
    rm -rf "$TMP/work"
    mkdir -p "$TMP/work/.github" "$TMP/work/charts/demo"
    cat > "$TMP/work/.github/release-config.json" <<'JSON'
{
  "images": [
    { "name": "amp-api",  "dir": "agent-manager-service" },
    { "name": "amp-observer", "dir": "agent-manager-observer" }
  ]
}
JSON
    cat > "$TMP/work/charts/demo/values.yaml" <<'YAML'
agentManagerService:
  image:
    repository: ghcr.io/wso2/amp-api
observer:
  image:
    repository: ghcr.io/wso2/amp-observer
# Same organization, different product: not ours to move.
gateway:
  controller:
    repository: ghcr.io/wso2/api-platform/gateway-controller
# Name that merely starts with one of ours.
authClient:
  repository: ghcr.io/wso2/amp-api-client
# Third-party.
thunder:
  registry: ghcr.io/thunder-id
buildpack:
  image: ghcr.io/openchoreo/buildpack/ballerina:18
# First-party but deliberately out of scope.
instrumentation:
  imageRepository: ghcr.io/wso2/amp-python-instrumentation-provider
YAML
}

run_rewrite() {
    ( cd "$TMP/work" && CONFIG_FILE=".github/release-config.json" CHARTS_DIR="charts" \
        bash "$SCRIPT" "$@" >"$TMP/out.txt" 2>&1 )
}

V="$TMP/work/charts/demo/values.yaml"

setup
if run_rewrite ghcr.io/wso2 registry.example.com/grp; then
    printf 'ok   - rewrite succeeds\n'
else
    printf 'FAIL - rewrite failed:\n'; sed 's/^/       /' "$TMP/out.txt"; FAILURES=$((FAILURES + 1))
fi
assert_contains "amp-api is repointed"      "$V" "registry.example.com/grp/amp-api"
assert_contains "amp-observer is repointed" "$V" "registry.example.com/grp/amp-observer"
# The dangerous ones.
assert_contains "the sibling product is left alone" "$V" "ghcr.io/wso2/api-platform/gateway-controller"
assert_contains "a name that only starts with ours is left alone" "$V" "ghcr.io/wso2/amp-api-client"
assert_contains "third-party thunder is left alone" "$V" "ghcr.io/thunder-id"
assert_contains "third-party openchoreo is left alone" "$V" "ghcr.io/openchoreo/buildpack/ballerina:18"
assert_contains "the instrumentation provider stays public" "$V" "ghcr.io/wso2/amp-python-instrumentation-provider"
# Anchored: amp-api-client legitimately still starts with this prefix, so only
# an exact end-of-value match means a first-party image was missed.
assert_absent_re "no first-party image is left on the old prefix" "$V" "ghcr\\.io/wso2/amp-api$"
assert_absent_re "amp-observer is not left behind either" "$V" "ghcr\\.io/wso2/amp-observer$"
# Rerunning with the now-stale prefix must fail rather than quietly do nothing:
# a release that continued would publish charts still pointing at the old
# registry, and that only shows up as a failed pull on a customer's cluster.
if run_rewrite ghcr.io/wso2 registry.example.com/grp; then
    printf 'FAIL - a second run matched nothing yet reported success\n'; FAILURES=$((FAILURES + 1))
else
    printf 'ok   - a run that matches nothing fails loudly\n'
fi

# A trailing slash on either argument must not double the separator.
setup
run_rewrite ghcr.io/wso2/ registry.example.com/grp/ || true
assert_contains "trailing slashes do not double the separator" "$V" "registry.example.com/grp/amp-api"
assert_absent   "no doubled slash is produced" "$V" "grp//amp-api"

if ((FAILURES > 0)); then
    printf '\n%d assertion(s) failed\n' "$FAILURES"
    exit 1
fi
printf '\nAll rewrite-image-registry assertions passed\n'
