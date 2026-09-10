#!/usr/bin/env bash
# Fail if an installer or a guide fetches this repository's own files over HTTP.
# Run: bash .github/scripts/check-no-raw-github-fetches.sh
#
# Those fetches are what the release bundle exists to replace. They break in
# three ways that are all invisible until an install is already running: GitHub
# rate-limits unauthenticated requests per IP and `helm --values <url>` does not
# retry on the 429, an egress-restricted cluster cannot reach the host at all,
# and a private mirror of this repository serves no public raw URL. Without a
# check they creep back in, because adding one is always the shortest diff.
#
# Two things are deliberately allowed:
#   - a markdown link, [text](url), which a reader clicks to view a file rather
#     than something an install fetches
#   - a defaulted variable, ${AMP_SCRIPT_BASE_URL:-...}, which a fork or an
#     air-gapped mirror can repoint in one place
#   - a shell comment, which documents the curl | bash usage of a script rather
#     than performing a fetch
set -uo pipefail

PATTERN='raw\.githubusercontent\.com/wso2/agent-manager'
FAILURES=0

check() {
  local label="$1" hits
  shift
  # Skip markdown links and defaulted variables; flag everything else.
  hits="$(grep -rn "$PATTERN" "$@" 2>/dev/null \
    | grep -v '](https://raw\.githubusercontent' \
    | grep -v ':-https://raw\.githubusercontent' \
    | grep -vE '^[^:]+:[0-9]+:[[:space:]]*#' \
    || true)"
  if [[ -n "$hits" ]]; then
    printf 'FAIL - %s fetches this repo over HTTP:\n' "$label"
    printf '%s\n' "$hits" | sed 's/^/       /'
    FAILURES=$((FAILURES + 1))
  else
    printf 'ok   - %s\n' "$label"
  fi
}

check "installers under deployments/" \
  --include='*.sh' --include='*.yaml' --include='*.yml' deployments
check "guides under documentation/docs/" \
  --include='*.mdx' --include='*.md' documentation/docs

# The current version snapshot is what the docs site serves, so it needs the same
# gate. Older snapshots are deliberately excluded: they are frozen records of
# what shipped, they still contain these URLs, and rewriting them would be
# falsifying history rather than fixing an install.
LATEST_VERSION="$(sed -n 's/^[[:space:]]*"\(v[0-9][^"]*\)".*/\1/p' documentation/versions.json 2>/dev/null | head -1)"
if [ -n "$LATEST_VERSION" ] && [ -d "documentation/versioned_docs/version-${LATEST_VERSION}" ]; then
  # The isolation-tier guides are excluded: the tarball published for this
  # version predates deployments/setup being added to the bundle, so those two
  # node installers genuinely have to be fetched there. A snapshot cannot be
  # pointed at files its own release asset does not contain.
  check "guides in the current snapshot (${LATEST_VERSION})" \
    --include='*.mdx' --include='*.md' \
    --exclude-dir=isolation-tiers \
    "documentation/versioned_docs/version-${LATEST_VERSION}"
else
  printf 'ok   - no current version snapshot to check\n'
fi

if ((FAILURES > 0)); then
  printf '\nUse the bundled copy instead: ${AMP_DIST}/deployments/... in a guide,\n'
  printf 'or a path relative to the script in an installer. If a fallback URL is\n'
  printf 'genuinely needed, put it behind a defaulted variable.\n'
  exit 1
fi
printf '\nNo raw-GitHub fetches of this repository\n'
