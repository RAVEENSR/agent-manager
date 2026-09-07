#!/usr/bin/env bash
# retag-image.sh — publish an already-released image under a new tag.
#
# `imagetools create` copies the manifest list rather than the layers, so the
# multi-arch structure survives, nothing is pulled or rebuilt, and the resulting
# digest is identical by construction. The digest comparison afterwards is the
# proof that a promoted release ships exactly the bits that were tested.
#
# Usage: retag-image.sh <image-ref-without-tag> <source-tag> <target-tag>
set -euo pipefail

IMAGE="${1:?usage: retag-image.sh <image-ref-without-tag> <source-tag> <target-tag>}"
SOURCE_TAG="${2:?usage: retag-image.sh <image-ref-without-tag> <source-tag> <target-tag>}"
TARGET_TAG="${3:?usage: retag-image.sh <image-ref-without-tag> <source-tag> <target-tag>}"

DOCKER="${DOCKER:-docker}"

"$DOCKER" buildx imagetools create -t "${IMAGE}:${TARGET_TAG}" "${IMAGE}:${SOURCE_TAG}"

digest_of() {
  "$DOCKER" buildx imagetools inspect --format '{{json .Manifest.Digest}}' "$1"
}

SOURCE_DIGEST="$(digest_of "${IMAGE}:${SOURCE_TAG}")"
TARGET_DIGEST="$(digest_of "${IMAGE}:${TARGET_TAG}")"

if [ "$SOURCE_DIGEST" != "$TARGET_DIGEST" ]; then
  {
    echo "Error: digest mismatch after retagging ${IMAGE}."
    echo "  ${IMAGE}:${SOURCE_TAG} -> ${SOURCE_DIGEST}"
    echo "  ${IMAGE}:${TARGET_TAG} -> ${TARGET_DIGEST}"
  } >&2
  exit 1
fi

echo "✅ ${IMAGE}:${TARGET_TAG} → ${SOURCE_DIGEST}"
