#!/usr/bin/env bash
# Build + run the appliance in podman with a headless X display + VNC.
#
#   ./scripts/podman-up.sh             — rebuild image and run in foreground
#   ./scripts/podman-up.sh --bg        — rebuild and detach
#   ./scripts/podman-up.sh --no-build  — skip rebuild (faster iteration)
#
# Once running:
#   - Web UI:  http://localhost:8089
#   - VNC:     vnc://localhost:5900   (no password, dev-only)
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_DIR"

BUILD=1
DETACH=0
for arg in "$@"; do
  case "$arg" in
    --no-build) BUILD=0 ;;
    --bg|--detach) DETACH=1 ;;
    *) echo "unknown flag: $arg" >&2; exit 1 ;;
  esac
done

if [[ $BUILD -eq 1 ]]; then
  echo "→ podman build…"
  podman-compose build
fi

# Stop any prior instance so port 8089/5900 don't double-bind.
podman-compose down 2>/dev/null || true

if [[ $DETACH -eq 1 ]]; then
  podman-compose up -d
  echo "→ container detached. Logs: podman logs -f little-jerrys"
else
  exec podman-compose up
fi
