#!/usr/bin/env bash
# Package a built raw image and the operator guide as release-ready assets.
# Usage: scripts/package-pi-release.sh IMAGE.img BASE_IMAGE.img [GUIDE.md] [OUT_DIR]
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"
err() { echo "error: $*" >&2; exit 1; }
[[ $# -ge 2 && $# -le 4 ]] || err "usage: $0 IMAGE.img BASE_IMAGE.img [GUIDE.md] [OUT_DIR]"
IMAGE=$(realpath "$1")
BASE=$(realpath "$2")
GUIDE=${3:-"$PROJECT_DIR/BURN-PI.md"}
[[ "$GUIDE" = /* ]] || GUIDE="$PROJECT_DIR/$GUIDE"
GUIDE=$(realpath "$GUIDE")
OUT=${4:-"$PROJECT_DIR/build/release"}
[[ "$OUT" = /* ]] || OUT="$PROJECT_DIR/$OUT"
OUT=$(realpath -m "$OUT")
[[ -f "$IMAGE" ]] || err "raw image not found: $IMAGE"
[[ -f "$BASE" ]] || err "base image not found: $BASE"
[[ -f "$GUIDE" ]] || err "setup guide not found: $GUIDE"
[[ "$IMAGE" != "$BASE" ]] || err "release image cannot be its own base image"
[[ "$IMAGE" == *.img ]] || err "release input must be an uncompressed .img file"

VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null || echo v0.0.0)}
[[ "$VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$ ]] || err "VERSION must contain 1-64 letters, digits, dots, pluses, underscores, or hyphens"
ASSET="little-jerrys-${VERSION}-pi4-arm64.img.xz"
XZ_THREADS=${XZ_THREADS:-2}
[[ "$XZ_THREADS" =~ ^[1-8]$ ]] || err "XZ_THREADS must be an integer from 1 through 8"
mkdir -p "$OUT"
for asset in "$OUT/$ASSET" "$OUT/BURN-PI.md" "$OUT/build-metadata.json" "$OUT/SHA256SUMS"; do
  [[ ! -e "$asset" ]] || err "refusing to overwrite release asset: $asset"
done
TMP=$(mktemp -d "$OUT/.package.XXXXXX")
cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  if (( rc != 0 )); then rm -rf "$TMP"; fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

log() { printf '\n=== %s ===\n' "$*"; }
log "compressing flashable raw image (xz -6, $XZ_THREADS threads)"
xz -T"$XZ_THREADS" -6 --check=crc64 --keep -c "$IMAGE" > "$TMP/$ASSET"
cp "$GUIDE" "$TMP/BURN-PI.md"
IMAGE_SHA=$(sha256sum "$IMAGE" | awk '{print $1}')
BASE_SHA=$(sha256sum "$BASE" | awk '{print $1}')
COMMIT=$(git rev-parse HEAD 2>/dev/null || echo unknown)
DIRTY=false
if [[ -n "$(git status --porcelain 2>/dev/null || true)" ]]; then DIRTY=true; fi
BUILT_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
MEDIA_MODE=${MEDIA_MODE:-sd}
[[ "$MEDIA_MODE" == sd ]] || err "only MEDIA_MODE=sd is supported; USB is a read-only import source"
python3 - "$TMP/build-metadata.json" "$VERSION" "$COMMIT" "$DIRTY" "$BUILT_AT" "$BASE_SHA" "$IMAGE_SHA" "$(stat -c %s "$IMAGE")" "$MEDIA_MODE" <<'PY'
import json, sys
_, version, commit, dirty, built_at, base_sha, image_sha, raw_bytes, media_mode = sys.argv[1:]
meta = {
  "product": "Little Jerry's",
  "version": version,
  "target": "Raspberry Pi 4 / ARM64",
  "commit": commit,
  "source_tree_dirty": dirty.lower() == "true",
  "built_at_utc": built_at,
  "base_image_sha256": base_sha,
  "raw_image_sha256": image_sha,
  "raw_image_bytes": int(raw_bytes),
  "media_mode": media_mode,
  "media_included": False,
  "runtime_dependencies": ["mpv", "ffmpeg", "exfatprogs", "ntfs-3g", "cloud-guest-utils"],
  "media_root": "/opt/jerry/media",
  "import_root": "/media/usb",
}
with open(sys.argv[1], "w", encoding="utf-8") as f:
  json.dump(meta, f, indent=2, sort_keys=True)
  f.write("\n")
PY

log "enforcing GitHub's per-asset size limit"
for asset in "$TMP/$ASSET" "$TMP/BURN-PI.md" "$TMP/build-metadata.json"; do
  bytes=$(stat -c %s "$asset")
  (( bytes < 2147483648 )) || err "asset exceeds 2 GiB: $(basename "$asset") ($bytes bytes)"
done
(cd "$TMP" && sha256sum "$ASSET" BURN-PI.md build-metadata.json > SHA256SUMS)
for asset in "$ASSET" BURN-PI.md build-metadata.json SHA256SUMS; do mv "$TMP/$asset" "$OUT/$asset"; done
rmdir "$TMP"
trap - EXIT INT TERM
log "release assets ready in $OUT"
ls -lh "$OUT/$ASSET" "$OUT/BURN-PI.md" "$OUT/build-metadata.json" "$OUT/SHA256SUMS"
