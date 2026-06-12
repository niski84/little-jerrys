#!/usr/bin/env bash
# little-jerrys — kill → compile → start → poll /api/health
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="$PROJECT_DIR/little-jerrys"
PID_FILE="$PROJECT_DIR/little-jerrys.pid"
PORT="${PORT:-8089}"
# Dev-mode defaults: stub the player and simulate the GPIO button so this
# runs cleanly on a non-Pi laptop. Override on the deployed Pi via systemd.
export JERRY_PLAYER_STUB="${JERRY_PLAYER_STUB:-1}"
export JERRY_GPIO_SIMULATED="${JERRY_GPIO_SIMULATED:-1}"
export JERRY_MEDIA_ROOT="${JERRY_MEDIA_ROOT:-/tmp/jerry-media}"
export JERRY_STATE_PATH="${JERRY_STATE_PATH:-/tmp/jerry-state.json}"
# Rescue password — always works for the admin user. Lets you recover from a
# locked-out state without editing JSON or running --reset-password. Override
# in production by setting JERRY_RESCUE_PASSWORD before invoking reload.sh.
export JERRY_RESCUE_PASSWORD="${JERRY_RESCUE_PASSWORD:-jerry-rescue}"
mkdir -p "$JERRY_MEDIA_ROOT"
LOG_FILE="$PROJECT_DIR/little-jerrys.log"

# Ensure Go toolchain is on PATH regardless of how this script was invoked.
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

echo "[little-jerrys] stopping..."
if [[ -f "$PID_FILE" ]]; then
  OLD_PID="$(cat "$PID_FILE")"
  kill "$OLD_PID" 2>/dev/null && sleep 0.5 || true
fi
# Kill the binary process but not this script (exclude bash/reload.sh matches)
pkill -f "^${BINARY}$" 2>/dev/null && sleep 0.3 || true
pkill -f "^\./$(basename "$BINARY")$" 2>/dev/null && sleep 0.3 || true

echo "[little-jerrys] compiling..."
cd "$PROJECT_DIR"
bash scripts/compile.sh

echo "[little-jerrys] starting on port $PORT..."
PORT="$PORT" nohup "$BINARY" > "$LOG_FILE" 2>&1 &
NEW_PID=$!
echo "$NEW_PID" > "$PID_FILE"

echo "[little-jerrys] waiting for /api/health..."
for i in $(seq 1 30); do
  sleep 0.5
  if ! kill -0 "$NEW_PID" 2>/dev/null; then
    echo "✗ Process $NEW_PID died (port conflict or crash)"
    tail -5 "$LOG_FILE" 2>/dev/null | sed 's/^/  /' || true
    exit 1
  fi
  if curl -sf "http://localhost:$PORT/api/health" >/dev/null 2>&1; then
    echo "✓ Server ready at http://localhost:$PORT"
    exit 0
  fi
done
echo "✗ Server did not start — check $LOG_FILE"
exit 1
