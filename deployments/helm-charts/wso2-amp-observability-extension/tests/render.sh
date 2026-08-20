#!/usr/bin/env bash
# Render assertions for wso2-amp-observability-extension.
# Run: bash deployments/helm-charts/wso2-amp-observability-extension/tests/render.sh
#
# Covers the derived auth values that plain `helm template` with default values
# cannot distinguish from hardcoded ones: oauth.authorizationServers falling back
# to auth.issuer, and publicUrl being appended to auth.audience. A wrong issuer
# or audience here is invisible at install time and surfaces only as the observer
# 401ing every traces request with "invalid issuer" (see issue #1424).
set -uo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAILURES=0

# env_value <name> [helm --set args...] -> the rendered value of container env var <name>
# A render failure is reported rather than silently becoming an empty value: this
# chart deliberately `fail`s on some inputs, so a crash must not look like a
# wrong value.
env_value() {
  local name="$1" rendered
  shift
  if ! rendered="$(helm template test-release "$CHART_DIR" "$@" 2>&1)"; then
    printf 'helm template failed: %s\n' "$rendered" >&2
    return 1
  fi
  awk -v n="$name" '
    $1 == "-" && $2 == "name:" && $3 == n { found = 1; next }
    found && $1 == "value:" {
      sub(/^[[:space:]]*value:[[:space:]]*/, "")
      gsub(/^"|"$/, "")
      print
      exit
    }
  ' <<<"$rendered"
}

assert_env() {
  local label="$1" name="$2" expected="$3"
  shift 3
  local actual
  actual="$(env_value "$name" "$@")"
  if [[ "$expected" == "$actual" ]]; then
    printf 'ok   - %s\n' "$label"
  else
    printf 'FAIL - %s\n      expected %s: %q\n      actual   %s: %q\n' \
      "$label" "$name" "$expected" "$name" "$actual"
    FAILURES=$((FAILURES + 1))
  fi
}

# Defaults must stay byte-identical to the pre-derivation literals, so that
# existing installs and quick-start (which passes no overrides) are unaffected.
assert_env "default audience keeps the k3d resource entry" \
  KEY_MANAGER_AUDIENCE "urn:wso2:amp,amp-api-client,am-obs-mcp,http://traces.amp.localhost:11080/"
assert_env "default authorization servers fall back to the default issuer" \
  OAUTH_AUTHORIZATION_SERVERS "http://thunder.amp.localhost:8080"

# The issue #1424 fix: one issuer override has to move the advertised
# authorization server too, or MCP clients discover an authorization server whose
# tokens this service then rejects.
assert_env "issuer override moves the advertised authorization server" \
  OAUTH_AUTHORIZATION_SERVERS "https://thunder.example.com" \
  --set amObserver.auth.issuer=https://thunder.example.com
assert_env "explicit authorization servers win over the issuer" \
  OAUTH_AUTHORIZATION_SERVERS "https://as.example.com" \
  --set amObserver.auth.issuer=https://thunder.example.com \
  --set amObserver.oauth.authorizationServers=https://as.example.com

# publicUrl carries the RFC 8707 resource identifier MCP tokens are minted with,
# so it must reach the audience list with exactly one trailing slash.
assert_env "publicUrl override is appended to the audience" \
  KEY_MANAGER_AUDIENCE "urn:wso2:amp,amp-api-client,am-obs-mcp,https://traces.example.com/" \
  --set amObserver.publicUrl=https://traces.example.com
assert_env "a publicUrl that already ends in a slash is not doubled" \
  KEY_MANAGER_AUDIENCE "urn:wso2:amp,amp-api-client,am-obs-mcp,https://traces.example.com/" \
  --set amObserver.publicUrl=https://traces.example.com/
assert_env "an audience that already lists the resource gains no duplicate" \
  KEY_MANAGER_AUDIENCE "amp,https://traces.example.com/" \
  --set amObserver.publicUrl=https://traces.example.com \
  --set 'amObserver.auth.audience=amp\,https://traces.example.com/'
assert_env "whitespace in the audience does not defeat the duplicate check" \
  KEY_MANAGER_AUDIENCE "amp,https://traces.example.com/" \
  --set amObserver.publicUrl=https://traces.example.com \
  --set 'amObserver.auth.audience=amp\, https://traces.example.com/'
assert_env "an empty publicUrl appends nothing and leaves no trailing comma" \
  KEY_MANAGER_AUDIENCE "urn:wso2:amp,amp-api-client,am-obs-mcp" \
  --set-string amObserver.publicUrl=
assert_env "a stray comma in the audience does not produce an empty entry" \
  KEY_MANAGER_AUDIENCE "amp,https://traces.example.com/" \
  --set amObserver.publicUrl=https://traces.example.com \
  --set 'amObserver.auth.audience=amp\,'

if ((FAILURES > 0)); then
  printf '\n%d assertion(s) failed\n' "$FAILURES"
  exit 1
fi
printf '\nAll render assertions passed\n'
