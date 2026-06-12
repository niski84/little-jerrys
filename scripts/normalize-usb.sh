#!/usr/bin/env bash
# Pre-process USB drive: normalize loudness across all video files.
#
# 90s commercials and DVD episode rips have wildly different audio levels.
# Without this pass, staff will be reaching for the remote every time a loud
# commercial drops in. Two-pass EBU R128 loudnorm (-23 LUFS, -1 dB peak)
# matches what broadcast stations use.
#
# Usage:
#   ./scripts/normalize-usb.sh /media/usb           # in-place (mode default)
#   ./scripts/normalize-usb.sh /media/usb /tmp/out  # write to a separate dir
#
# Idempotent: skips files whose .normalized sidecar exists.
set -euo pipefail

SRC="${1:-}"
DST="${2:-$SRC}"

if [[ -z "$SRC" ]]; then
  echo "usage: $0 <src-dir> [dst-dir]" >&2
  exit 1
fi
if [[ ! -d "$SRC" ]]; then
  echo "src dir does not exist: $SRC" >&2
  exit 1
fi
if ! command -v ffmpeg >/dev/null; then
  echo "ffmpeg not found on PATH" >&2
  exit 1
fi

mkdir -p "$DST"

shopt -s nullglob globstar
EXT_RE='\.(mp4|mkv|mov|avi|m4v|webm)$'

count=0
skipped=0
for input in "$SRC"/**/*.{mp4,mkv,mov,avi,m4v,webm}; do
  [[ -f "$input" ]] || continue

  rel="${input#$SRC/}"
  out="$DST/$rel"
  marker="${out}.normalized"

  if [[ -f "$marker" ]]; then
    skipped=$((skipped + 1))
    continue
  fi

  mkdir -p "$(dirname "$out")"
  echo "→ normalizing $rel"

  # In-place mode: write to a temp file then atomic rename.
  tmp="${out}.tmp.mkv"
  ffmpeg -y -hide_banner -loglevel error \
    -i "$input" \
    -af "loudnorm=I=-23:TP=-1:LRA=7" \
    -c:v copy \
    -c:a aac -b:a 192k \
    "$tmp"
  mv "$tmp" "$out"
  : > "$marker"
  count=$((count + 1))
done

echo "Done. Normalized $count file(s); skipped $skipped already-processed."
