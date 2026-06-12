#!/usr/bin/env bash
# Atomic binary swap + systemd restart for Little Jerry's OTA updates.
#
# Called by the updater goroutine when the safe window is reached.
# Must run as root (jerry.service has sudo rights to this script only).
#
# Arguments:
#   $1  New version tag, e.g. "v0.2.0" (logged only)
#
# Exit codes:
#   0   Swap succeeded; systemd will restart the process immediately
#   1   Something went wrong; running binary is untouched

set -euo pipefail

VERSION="${1:-unknown}"
INSTALL_DIR="/opt/jerry"
BINARY="$INSTALL_DIR/jerry"
STAGED="$INSTALL_DIR/jerry.staged"
BACKUP="$INSTALL_DIR/jerry.prev"
PENDING="$INSTALL_DIR/update.pending"
SOCKET="/tmp/mpv-jerry.sock"
STARTUP_TIMEOUT=30  # seconds to wait for the new binary to open its socket
LOG_FILE="/var/log/jerry-update.log"
LOG_MAX_LINES=500   # keep the last N lines so the SD card doesn't fill up

log() {
  echo "[update-apply] $*" | tee -a "$LOG_FILE"
}

truncate_log() {
  if [[ -f "$LOG_FILE" ]]; then
    local lines
    lines=$(wc -l < "$LOG_FILE")
    if [[ "$lines" -gt "$LOG_MAX_LINES" ]]; then
      tail -n "$LOG_MAX_LINES" "$LOG_FILE" > "${LOG_FILE}.tmp" && mv "${LOG_FILE}.tmp" "$LOG_FILE"
    fi
  fi
}

truncate_log

log "applying $VERSION"

if [[ ! -f "$STAGED" ]]; then
  log "ERROR: staged binary not found at $STAGED"
  exit 1
fi

if [[ ! -x "$STAGED" ]]; then
  chmod +x "$STAGED"
fi

# --- Rollback helper ---
rollback() {
  log "ROLLBACK: restoring $BACKUP → $BINARY"
  if [[ -f "$BACKUP" ]]; then
    cp -f "$BACKUP" "$BINARY"
    chmod +x "$BINARY"
    systemctl restart jerry || true
    log "rollback complete — running previous version"
  else
    log "ERROR: no backup found, cannot roll back"
  fi
}

# --- 1. Back up running binary ---
cp -f "$BINARY" "$BACKUP"
log "backed up current binary to $BACKUP"

# --- 2. Atomic swap ---
# mv is atomic on the same filesystem (rename(2)); jerry.staged was written
# to the same directory so this never has a partial-binary window.
mv -f "$STAGED" "$BINARY"
chmod +x "$BINARY"
log "swapped binary"

# --- 3. Restart via systemd ---
log "restarting jerry.service"
systemctl restart jerry

# --- 4. Health check — wait for mpv IPC socket to appear ---
log "waiting up to ${STARTUP_TIMEOUT}s for new binary to come up…"
for i in $(seq 1 "$STARTUP_TIMEOUT"); do
  if [[ -S "$SOCKET" ]]; then
    log "socket appeared after ${i}s — update successful"
    rm -f "$PENDING" "$BACKUP"
    log "done — now running $VERSION"
    truncate_log
    exit 0
  fi
  sleep 1
done

# Socket never appeared — new binary is broken.
log "ERROR: new binary did not open $SOCKET within ${STARTUP_TIMEOUT}s"
rollback
exit 1
