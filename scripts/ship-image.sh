#!/usr/bin/env bash
# Build and push the Little Jerry's container image to GHCR so my
# brother (or anyone else) can pull it with `podman compose up` — no clone,
# no build chain on their workstation.
#
# Usage:
#   ./scripts/ship-image.sh                 # build only, tag as :dev (local)
#   ./scripts/ship-image.sh push            # build + push :dev to GHCR
#   TAG=v1 ./scripts/ship-image.sh push     # build + push :v1 to GHCR
#   TAG=latest ./scripts/ship-image.sh push # build + push :latest (Brian's tag)
#
# Auth: you need to be logged into ghcr.io once, e.g.
#   echo "$GHCR_TOKEN" | podman login ghcr.io -u niski84 --password-stdin
# (Token needs `write:packages` scope.)

set -euo pipefail

GHCR_USER="${GHCR_USER:-niski84}"
IMAGE="ghcr.io/${GHCR_USER}/little-jerrys"
TAG="${TAG:-dev}"

# Prefer podman, fall back to docker. Brian uses podman; we want parity.
RUNTIME="${RUNTIME:-$(command -v podman 2>/dev/null || command -v docker)}"
if [[ -z "$RUNTIME" ]]; then
  echo "error: need podman or docker on PATH" >&2
  exit 1
fi

cd "$(dirname "$0")/.."

echo "=== Building ${IMAGE}:${TAG} (linux/amd64) via $RUNTIME ==="
"$RUNTIME" build --platform linux/amd64 -t "${IMAGE}:${TAG}" -f Containerfile .
echo "✓ Build complete"

# Build context + intermediate Go layers can chew through several GB if you
# rebuild often. Prune unreferenced layers after each build.
echo "=== Pruning dangling layers ==="
"$RUNTIME" image prune -f
echo "✓ Pruned"

if [[ "${1:-}" == "push" ]]; then
  echo "=== Pushing ${IMAGE}:${TAG} ==="
  "$RUNTIME" push "${IMAGE}:${TAG}"
  echo "✓ Pushed"
  echo
  echo "Brian's pull command:"
  echo "  podman pull ${IMAGE}:${TAG}"
fi
