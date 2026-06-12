#!/usr/bin/env bash
# Real smoke test for the playback path. Catches the bug class where the
# admin UI looks fine but mpv isn't actually rendering pixels.
#
# What it verifies (and why each one matters):
#
#   1. Binary's "now playing" matches mpv's window title
#      → catches silent loadfile failures (the divergence-in-state bug)
#   2. mpv X11 window covers the full canvas
#      → catches "fullscreen flag ignored, video centered with black bars"
#   3. The captured screenshot has actual color (not all-black)
#      → catches "mpv is loaded but stuck in idle / decoder failed"
#   4. After /api/skip, all three layers update in lockstep
#      → catches racey commands and dropped IPC messages
#   5. After seeking near EOF, exactly one auto-advance fires
#      → catches the eventLoop double-fire / missing debounce
#
# Usage:
#   ./scripts/smoke-playback.sh
#
# Requires: jq, the dev container running on :8089 with VNC at :5901,
# a few real .mp4 files in compose-data/media.

set -euo pipefail

PASS=0
FAIL=0
JAR="$(mktemp)"
SCREENSHOT_DIR="$(mktemp -d)"
trap 'rm -rf "$JAR" "$SCREENSHOT_DIR"' EXIT

ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
fail() { printf '  \033[31m✗\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
hdr()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }

CONTAINER="${CONTAINER:-little-jerrys}"
HOST_URL="${HOST_URL:-http://127.0.0.1:8089}"
PASSWORD="${PASSWORD:-jerry-rescue}"

api() {
  local method="$1" path="$2"
  shift 2
  curl -fsS -b "$JAR" -c "$JAR" -X "$method" "$@" "$HOST_URL$path"
}

# ─── 0. Setup ───────────────────────────────────────────────────────────
hdr "0. Login + prime queue"
http=$(curl -fsS -c "$JAR" -d "user=admin&password=$PASSWORD" \
       "$HOST_URL/login" -o /dev/null -w '%{http_code}')
if [[ "$http" == "303" || "$http" == "302" ]]; then
  ok "login (rescue) returned $http"
else
  fail "login failed: $http"; exit 1
fi
api POST /api/queue/regenerate -o /dev/null
api POST /api/skip -o /dev/null
sleep 3

# ─── 1. Three-layer state agreement ─────────────────────────────────────
hdr "1. Binary ↔ mpv ↔ X11 agree on what's playing"
binary_now=$(api GET /api/status | jq -r '.now_filename')
mpv_window=$(podman exec "$CONTAINER" sh -c \
  'DISPLAY=:0 xwininfo -root -tree 2>&1' \
  | grep -oE 's[0-9]+e[0-9]+_[a-z0-9_]+\.mp4' | head -1)
if [[ "$binary_now" == "$mpv_window" ]]; then
  ok "binary=$binary_now matches mpv window"
else
  fail "binary=$binary_now BUT mpv window=$mpv_window"
fi

# ─── 2. mpv window covers the full canvas ──────────────────────────────
hdr "2. mpv window is fullscreen (1920x1080+0+0)"
geom=$(podman exec "$CONTAINER" sh -c \
  'DISPLAY=:0 xwininfo -root -tree 2>&1' \
  | grep -oE '[0-9]+x[0-9]+\+[0-9]+\+[0-9]+' | grep -v '^1x1' | head -1)
if [[ "$geom" == "1920x1080+0+0" ]]; then
  ok "mpv geometry = $geom"
else
  fail "mpv geometry = $geom (expected 1920x1080+0+0)"
fi

# ─── 3. Screenshot has actual color content (not all black) ────────────
hdr "3. Screenshot shows real video pixels"
podman exec "$CONTAINER" sh -c \
  'DISPLAY=:0 ffmpeg -y -loglevel error -f x11grab -video_size 1920x1080 -i :0 -frames:v 1 /tmp/smoke.png' \
  >/dev/null 2>&1
podman cp "$CONTAINER:/tmp/smoke.png" "$SCREENSHOT_DIR/before.png" 2>/dev/null
# A black frame compresses to ~5KB; a real frame with our test pattern
# is hundreds of KB.
size=$(stat -c%s "$SCREENSHOT_DIR/before.png" 2>/dev/null || echo 0)
if (( size > 50000 )); then
  ok "screenshot is $size bytes (real frame, not black)"
else
  fail "screenshot is only $size bytes — likely all black"
  echo "      saved screenshot: $SCREENSHOT_DIR/before.png"
fi

# ─── 4. Skip propagates to all three layers ────────────────────────────
hdr "4. /api/skip updates binary AND mpv AND X11 in lockstep"
prev_binary="$binary_now"
api POST /api/skip -o /dev/null
sleep 2
new_binary=$(api GET /api/status | jq -r '.now_filename')
new_mpv=$(podman exec "$CONTAINER" sh -c \
  'DISPLAY=:0 xwininfo -root -tree 2>&1' \
  | grep -oE 's[0-9]+e[0-9]+_[a-z0-9_]+\.mp4' | head -1)
if [[ "$new_binary" != "$prev_binary" ]]; then
  ok "binary advanced: $prev_binary → $new_binary"
else
  fail "binary did NOT advance after skip"
fi
if [[ "$new_mpv" == "$new_binary" ]]; then
  ok "mpv window advanced to match: $new_mpv"
else
  fail "mpv window=$new_mpv but binary=$new_binary"
fi

# ─── 5. Auto-advance fires exactly once on EOF ─────────────────────────
hdr "5. Auto-advance fires exactly once at end-of-track"
# Seek to near end so we can observe the transition quickly.
dur=$(api GET /api/status | jq -r '.duration_secs')
target=$(echo "$dur - 2" | bc)
api POST /api/seek --data "secs=$target" -o /dev/null
# Snapshot event-log size, wait for natural EOF, count new auto-advances.
before_count=$(api GET '/api/events/recent?n=50' \
  | jq '[.events[] | select(.message | contains("track ended"))] | length')
sleep 5
track_after=$(api GET /api/status | jq -r '.now_filename')
after_count=$(api GET '/api/events/recent?n=50' \
  | jq '[.events[] | select(.message | contains("track ended"))] | length')
delta=$((after_count - before_count))
if [[ "$track_after" != "$new_binary" ]]; then
  ok "auto-advance happened: $new_binary → $track_after"
else
  fail "track did NOT auto-advance"
fi
if [[ "$delta" -eq 1 ]]; then
  ok "exactly 1 auto-advance event fired (debounce holds)"
elif [[ "$delta" -eq 0 ]]; then
  fail "no auto-advance event fired"
else
  fail "$delta auto-advance events fired (debounce broken — should be 1)"
fi

# ─── 6. Regenerate starts playing the new head ─────────────────────────
hdr "6. Regenerate immediately plays the queue head"
api POST /api/queue/regenerate -o /dev/null
sleep 2
state=$(api GET /api/status)
now_after=$(echo "$state" | jq -r '.now_filename')
head_after=$(echo "$state" | jq -r '.upcoming[0].name')
mpv_after=$(podman exec "$CONTAINER" sh -c \
  'DISPLAY=:0 xwininfo -root -tree 2>&1' \
  | grep -oE 's[0-9]+e[0-9]+_[a-z0-9_]+\.mp4' | head -1)
# After regenerate, we expect:
#   binary's "now" is set to the track that just started
#   binary's queue head is the SECOND item (head was just popped to play)
#   mpv window shows the same track as binary's "now"
if [[ -n "$now_after" && "$now_after" != "null" ]]; then
  ok "binary now=$now_after (not empty)"
else
  fail "binary now is empty after regenerate"
fi
if [[ "$mpv_after" == "$now_after" ]]; then
  ok "mpv window matches binary now: $mpv_after"
else
  fail "mpv=$mpv_after BUT binary now=$now_after — preview will look wrong"
fi
if [[ "$now_after" != "$head_after" ]]; then
  ok "queue head ($head_after) is what plays NEXT, not what's playing NOW"
else
  fail "queue head equals now-playing — head wasn't popped on play"
fi

# ─── 7. Queue mutations hit disk before the request returns ────────────
hdr "7. Queue persists to state.json on every mutation"
expected=$(api GET /api/status | jq -c '[.upcoming[].name]')
on_disk=$(podman exec "$CONTAINER" cat /var/lib/jerry/state.json 2>/dev/null \
  | jq -c '[.queue_items[].path | split("/") | last]')
if [[ "$expected" == "$on_disk" ]]; then
  ok "in-memory queue matches state.json: $expected"
else
  fail "in-memory=$expected"
  fail "on-disk =$on_disk"
fi
# Mutate (skip), check disk reflects it without delay.
api POST /api/skip -o /dev/null
new_expected=$(api GET /api/status | jq -c '[.upcoming[].name]')
new_on_disk=$(podman exec "$CONTAINER" cat /var/lib/jerry/state.json 2>/dev/null \
  | jq -c '[.queue_items[].path | split("/") | last]')
if [[ "$new_expected" == "$new_on_disk" ]]; then
  ok "after skip: in-memory and on-disk both = $new_on_disk"
else
  fail "after skip: in-memory=$new_expected"
  fail "             on-disk =$new_on_disk"
fi

# ─── Summary ───────────────────────────────────────────────────────────
hdr "Summary"
printf '  passed: \033[32m%d\033[0m   failed: \033[31m%d\033[0m\n' "$PASS" "$FAIL"
exit $((FAIL > 0 ? 1 : 0))
