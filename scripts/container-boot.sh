#!/usr/bin/env bash
# Container entrypoint. Brings up:
#   1. Xvfb on :0 — virtual framebuffer (no real display in container)
#   2. x11vnc on :5900 → mirrors :0 so we can VNC in and see what mpv shows
#   3. The Go binary, which spawns mpv (which renders to :0)
#
# Cleanup: SIGTERM/SIGINT propagates to all three so `podman stop` is clean.
set -euo pipefail

log() { printf '[boot] %s\n' "$*"; }

mkdir -p /var/lib/jerry /media/usb

# Clean stale X locks left from a previous run — otherwise Xvfb refuses to
# start on container restart and the boot loops silently.
rm -f /tmp/.X*-lock 2>/dev/null || true
rm -rf /tmp/.X11-unix 2>/dev/null || true
mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix

log "starting Xvfb on :0 (1920x1080x24)"
Xvfb :0 -screen 0 1920x1080x24 -ac >/var/log/xvfb.log 2>&1 &
XVFB_PID=$!

# Wait for Xvfb to be ready before starting mpv (otherwise mpv crashes on
# DISPLAY connect). xdpyinfo is the canonical readiness probe.
for i in $(seq 1 50); do
  if xdpyinfo -display :0 >/dev/null 2>&1; then
    log "Xvfb ready"
    break
  fi
  sleep 0.1
done

log "starting x11vnc on :5900 (passwordless — appliance is on a private LAN)"
x11vnc -display :0 -forever -nopw -shared -rfbport 5900 -bg -o /var/log/x11vnc.log

cleanup() {
  log "shutting down"
  pkill -TERM -P $$ 2>/dev/null || true
  kill "$XVFB_PID" 2>/dev/null || true
  exit 0
}
trap cleanup TERM INT

log "starting little-jerrys (web on :$PORT, media at $JERRY_MEDIA_ROOT)"
exec /opt/jerry/little-jerrys
