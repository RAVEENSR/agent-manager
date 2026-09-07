#!/usr/bin/env bash
# resolve-docs-version.sh — print the documentation version a release points at.
#
# Mirrors the branch order in update-helm-charts.sh so the promotion workflow can
# enforce the same rule *before* it cuts any branches or tags. Keep the two in
# sync: a divergence means the promotion passes its pre-flight and then fails
# halfway through, after tags have been pushed.
#
# The two copies do not run from the same commit. This mirror runs from main (the
# validate job), while update-helm-charts.sh runs from the release candidate's own
# base commit (the prepare-promotion job) — an arbitrarily old tree. So editing
# both files in one commit does not make them agree at run time: this file's rule
# is checked against whatever update-helm-charts.sh said back when the rc was cut.
# When they disagree, validate passes and the stamp fails after the branch and both
# tags are pushed, requiring the cleanup runbook in release-promote.yml.
#
# Prints nothing (exit 0) for releases that cut no docs version.
# Usage: resolve-docs-version.sh <target-version>
set -euo pipefail

TARGET_VERSION="${1:?usage: resolve-docs-version.sh <target-version>}"

if [[ "$TARGET_VERSION" =~ (-|\.)rc[0-9]+$ ]]; then
  : # release candidates follow /docs/latest
elif [[ "$TARGET_VERSION" == *-dev* ]]; then
  : # nightlies follow /docs/latest
elif [[ "$TARGET_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "v${TARGET_VERSION%.*}.x"
else
  echo "v$TARGET_VERSION"
fi
