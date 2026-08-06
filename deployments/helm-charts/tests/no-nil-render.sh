#!/usr/bin/env bash
# Asserts that a chart never renders a Go-template zero value into a shipped
# manifest. Run: bash deployments/helm-charts/tests/no-nil-render.sh <chart-dir>
#
# This exists because of a defect that shipped. An External Secrets placeholder
# was written into three builder templates unescaped, so Helm evaluated
# `.registrysecret` — a key that exists only in the ExternalSecret's own
# templating context, not in the chart's — and emitted
#
#     .dockerconfigjson: <nil>
#
# The manifests stayed valid YAML, the charts linted, kubeconform passed, and
# every agent image push against an authenticated registry failed. It was
# invisible on k3d, whose registry needs no auth, so three runs of scripted
# local testing never hit it and it reached a cloud cluster instead.
#
# The bug rendered at default values, which is why a plain default render is the
# assertion. Unlike the per-chart tests/render.sh files, this runs for EVERY
# chart: the point is to catch a template nobody thought to write a case for.
set -uo pipefail

if [[ $# -ne 1 ]]; then
  printf 'usage: %s <chart-dir>\n' "${0##*/}" >&2
  exit 2
fi

CHART_DIR="${1%/}"
CHART_NAME="${CHART_DIR##*/}"

if [[ ! -f "$CHART_DIR/Chart.yaml" ]]; then
  printf 'not a chart directory (no Chart.yaml): %s\n' "$CHART_DIR" >&2
  exit 2
fi

FAILURES=0

# The three ways Helm surfaces a template value that resolved to nothing.
# `<nil>` comes from printing a nil interface, `<no value>` from a missing map
# key under text/template, and `%!s(` from a Printf verb applied to the wrong
# type — all of them mean a manifest shipped a placeholder instead of a value.
MARKERS=('<nil>' '<no value>' '%!s(')

# scan <label> — reads a rendered manifest stream on stdin and reports every line
# carrying a marker, attributed to the template that produced it. helm emits a
# `# Source: <path>` comment ahead of each document, so tracking the most recent
# one names the offending file without re-rendering per template.
scan() {
  awk -v label="$1" -v m1="${MARKERS[0]}" -v m2="${MARKERS[1]}" -v m3="${MARKERS[2]}" '
    /^# Source: / { src = $3; next }
    {
      hit = ""
      if (index($0, m1)) hit = m1
      else if (index($0, m2)) hit = m2
      else if (index($0, m3)) hit = m3
      if (hit == "") next
      bad++
      line = $0
      sub(/^[[:space:]]+/, "", line)
      printf "     %s in %s\n       %s\n", hit, (src == "" ? "(no Source comment)" : src), line
    }
    END { exit (bad > 0) }
  '
}

# render_case <label> [helm --set args...]
# A render that fails to execute is reported as a failure of its own rather than
# becoming an empty stream — otherwise a broken template would read as "no
# placeholder found", which is the opposite of the truth.
render_case() {
  local label="$1" rendered
  shift
  if ! rendered="$(helm template test-release "$CHART_DIR" "$@" 2>&1)"; then
    printf 'FAIL - %s: helm template failed\n%s\n' "$label" "$rendered"
    if [[ "$rendered" == *"found in Chart.yaml, but missing in charts/"* ]]; then
      printf '     hint: helm dependency build %s\n' "$CHART_DIR"
    fi
    FAILURES=$((FAILURES + 1))
    return
  fi
  if printf '%s\n' "$rendered" | scan "$label"; then
    printf 'ok   - %s renders no template zero values\n' "$label"
  else
    printf 'FAIL - %s renders a template zero value\n' "$label"
    FAILURES=$((FAILURES + 1))
  fi
}

render_case "$CHART_NAME (default values)"

# Optional per-chart extra renders, one line of `helm --set` arguments each, for
# charts whose templates are gated behind a value and so are absent from the
# default render. Blank lines and # comments are ignored.
CASES_FILE="$CHART_DIR/tests/render-cases.txt"
if [[ -f "$CASES_FILE" ]]; then
  case_no=0
  while IFS= read -r case_args || [[ -n "$case_args" ]]; do
    [[ -z "${case_args// /}" || "${case_args#\#}" != "$case_args" ]] && continue
    case_no=$((case_no + 1))
    # Word-split deliberately: the file holds shell-style argument lists.
    # shellcheck disable=SC2086
    render_case "$CHART_NAME (case $case_no: $case_args)" $case_args
  done <"$CASES_FILE"
fi

if ((FAILURES > 0)); then
  printf '\n%d of %s render check(s) failed\n' "$FAILURES" "$CHART_NAME"
  exit 1
fi
printf '\n%s: no template zero values rendered\n' "$CHART_NAME"
