#!/usr/bin/env bash
# assert-docs-version-published.sh — fail unless the docs version this release
# needs has already been cut and merged.
#
# update-helm-charts.sh enforces the same thing, but only once the release branch
# and tags already exist. Running it in the promotion's validate job turns a
# half-finished release into a fast, actionable failure.
#
# Usage: assert-docs-version-published.sh <docs-version> <versions-json> <target-version>
set -euo pipefail

DOCS_VERSION="${1-}"
VERSIONS_FILE="${2:?usage: assert-docs-version-published.sh <docs-version> <versions-json> <target-version>}"
TARGET_VERSION="${3:?usage: assert-docs-version-published.sh <docs-version> <versions-json> <target-version>}"

if [ -z "$DOCS_VERSION" ]; then
  echo "No documentation version is required for $TARGET_VERSION; the console will follow /docs/latest"
  exit 0
fi

if [ ! -f "$VERSIONS_FILE" ]; then
  echo "Error: documentation manifest $VERSIONS_FILE is missing, so $DOCS_VERSION cannot be verified." >&2
  exit 1
fi

if ! grep -q "\"$DOCS_VERSION\"" "$VERSIONS_FILE"; then
  {
    echo "Error: documentation version $DOCS_VERSION is not published."
    echo
    echo "Run the \"Documentation Release\" workflow with:"
    echo "  version=$DOCS_VERSION"
    echo "  docker_tag=v$TARGET_VERSION"
    echo "then merge the pull request it opens and re-run this promotion."
  } >&2
  exit 1
fi

echo "✅ Documentation version $DOCS_VERSION is published"
