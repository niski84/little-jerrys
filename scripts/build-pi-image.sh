#!/usr/bin/env bash
# Bake the Little Jerry's into a Raspberry Pi OS Lite (64-bit)
# image, producing a flashable .img Brian can write to an SD card.
#
# Inputs:
#   $1  Path to a Raspberry Pi OS Lite arm64 .img (uncompressed). Download
#       from https://www.raspberrypi.com/software/operating-systems/.
#       Default: ./build/pi-os-lite-arm64.img
#
# Output:
#   ./build/little-jerrys-YYYYMMDD.img
#
# What it does (all done via loopback mount, no chroot needed):
#   1. Cross-compile the arm64 binary if it's not already built
#   2. Copy the raw input image to the output path
#   3. Set up loopback devices for the boot + root partitions
#   4. Mount root, copy:
#        /opt/jerry/jerry                    (the binary)
#        /opt/jerry/firstboot.sh
#        /etc/systemd/system/jerry.service
#        /etc/systemd/system/jerry-firstboot.service
#        /etc/NetworkManager/system-connections/jerry-ap.nmconnection
#        /etc/udev/rules.d/99-jerry-usb.rules
#        /etc/mpv/mpv.conf
#      and `systemctl enable jerry jerry-firstboot` via symlink.
#   5. Mount boot, drop a `userconf.txt` so SSH can be enabled if the
#      operator drops `ssh` into the boot partition.
#   6. Unmount cleanly + detach loop device.
#
# Requires sudo for losetup + mount. Doesn't need qemu-user-static.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

INPUT_IMG="${1:-build/pi-os-lite-arm64.img}"
DATE_STAMP="$(date +%Y%m%d)"
OUTPUT_IMG="build/little-jerrys-${DATE_STAMP}.img"
BINARY="build/jerry-arm64"
# Optional: drop .mp4/.mkv files into build/media/ (episodes) and
# build/media/commercials/ (ads) before running this script. They will be
# baked into the image at /opt/jerry/media — no USB drive required on the Pi.
MEDIA_SRC="build/media"

err() { printf 'error: %s\n' "$*" >&2; exit 1; }
log() { printf '\n=== %s ===\n' "$*"; }

# --- Preflight ---
[[ "$(id -u)" -ne 0 ]] || err "don't run this whole script as root; it will sudo where needed"
command -v losetup >/dev/null   || err "losetup missing (install util-linux)"
command -v partprobe >/dev/null || err "partprobe missing (install parted)"

if [[ ! -f "$INPUT_IMG" ]]; then
  log "Pi OS Lite not found — downloading (≈500 MB, one-time)"
  mkdir -p build
  # Stable redirect URL from the Pi Foundation. Always resolves to latest Bookworm Lite arm64.
  PI_OS_URL="https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2024-11-19/2024-11-19-raspios-bookworm-arm64-lite.img.xz"
  XZ_PATH="build/pi-os-lite-arm64.img.xz"
  curl -L --progress-bar -o "$XZ_PATH" "$PI_OS_URL"
  log "decompressing Pi OS image (this takes ~1 min)"
  xz -d "$XZ_PATH"
  mv "build/2024-11-19-raspios-bookworm-arm64-lite.img" "$INPUT_IMG" 2>/dev/null || \
    mv build/*.img "$INPUT_IMG"
  log "Pi OS Lite ready at $INPUT_IMG"
fi
command -v xz >/dev/null || err "xz missing — install xz-utils (apt install xz-utils)"
[[ -f "$INPUT_IMG" ]] || err "base image still missing after download attempt: $INPUT_IMG"

# --- 1. Cross-compile if needed ---
if [[ ! -x "$BINARY" ]]; then
  log "cross-compiling Pi binary"
  "$SCRIPT_DIR/cross-compile-pi.sh"
fi
[[ -x "$BINARY" ]] || err "binary missing after cross-compile: $BINARY"

# --- 1b. Measure media files if provided ---
MEDIA_BYTES=0
BAKE_MEDIA=false
if [[ -d "$MEDIA_SRC" ]]; then
  shopt -s nullglob
  mapfile -t MEDIA_FILES < <(find "$MEDIA_SRC" -type f \( \
    -iname "*.mp4" -o -iname "*.mkv" -o -iname "*.mov" \
    -o -iname "*.avi" -o -iname "*.m4v" -o -iname "*.webm" \
  \))
  shopt -u nullglob
  if [[ ${#MEDIA_FILES[@]} -gt 0 ]]; then
    BAKE_MEDIA=true
    MEDIA_BYTES=$(du -sb "$MEDIA_SRC" | awk '{print $1}')
    MEDIA_MB=$(( (MEDIA_BYTES / 1024 / 1024) + 256 ))  # +256 MB headroom
    log "found ${#MEDIA_FILES[@]} media file(s) in $MEDIA_SRC ($(( MEDIA_BYTES / 1024 / 1024 )) MB) — baking into image"
  fi
fi

# --- 2. Copy base image and expand if baking media ---
log "copying base image → $OUTPUT_IMG"
cp --reflink=auto "$INPUT_IMG" "$OUTPUT_IMG"

if $BAKE_MEDIA; then
  log "expanding image by ${MEDIA_MB} MB to fit media"
  truncate -s "+${MEDIA_MB}M" "$OUTPUT_IMG"
  # Expand the root (ext4) partition to fill the new space.
  # We use a temp loopback just for the resize before the main mount.
  LOOP_RESIZE="$(sudo losetup --show -fP "$OUTPUT_IMG")"
  sudo partprobe "$LOOP_RESIZE"
  sudo e2fsck -f -y "${LOOP_RESIZE}p2" || true
  sudo resize2fs "${LOOP_RESIZE}p2"
  sudo losetup -d "$LOOP_RESIZE"
fi

# --- 3. Loopback mount ---
log "loop-mounting partitions"
LOOP="$(sudo losetup --show -fP "$OUTPUT_IMG")"
sudo partprobe "$LOOP"

# Pi OS images have two partitions: 1 = vfat /boot, 2 = ext4 /
BOOT_PART="${LOOP}p1"
ROOT_PART="${LOOP}p2"

MNT_BOOT="$(mktemp -d)"
MNT_ROOT="$(mktemp -d)"

cleanup() {
  log "cleanup"
  sudo umount "$MNT_BOOT" 2>/dev/null || true
  sudo umount "$MNT_ROOT" 2>/dev/null || true
  [[ -n "${LOOP:-}" ]] && sudo losetup -d "$LOOP" 2>/dev/null || true
  rmdir "$MNT_BOOT" "$MNT_ROOT" 2>/dev/null || true
}
trap cleanup EXIT

sudo mount "$ROOT_PART" "$MNT_ROOT"
sudo mount "$BOOT_PART" "$MNT_BOOT"

# --- 4. Inject everything into root partition ---
log "injecting binary + service files"

sudo install -d -m 0755 "$MNT_ROOT/opt/jerry"
sudo install -m 0755 "$BINARY" "$MNT_ROOT/opt/jerry/jerry"
sudo install -m 0755 deploy/firstboot/firstboot.sh "$MNT_ROOT/opt/jerry/firstboot.sh"

sudo install -m 0644 deploy/systemd/jerry.service \
  "$MNT_ROOT/etc/systemd/system/jerry.service"
sudo install -m 0644 deploy/firstboot/jerry-firstboot.service \
  "$MNT_ROOT/etc/systemd/system/jerry-firstboot.service"

sudo install -d -m 0755 "$MNT_ROOT/etc/NetworkManager/system-connections"
sudo install -m 0600 deploy/networkmanager/jerry-ap.nmconnection \
  "$MNT_ROOT/etc/NetworkManager/system-connections/jerry-ap.nmconnection"

sudo install -d -m 0755 "$MNT_ROOT/etc/udev/rules.d"
sudo install -m 0644 deploy/firstboot/jerry-usb.rules \
  "$MNT_ROOT/etc/udev/rules.d/99-jerry-usb.rules"

sudo install -d -m 0755 "$MNT_ROOT/etc/mpv"
sudo install -m 0644 deploy/firstboot/mpv.conf "$MNT_ROOT/etc/mpv/mpv.conf"

# --- 4b. Bake media files into /opt/jerry/media (if provided) ---
if $BAKE_MEDIA; then
  log "copying media files into image (this may take a while)"
  sudo install -d -m 0755 "$MNT_ROOT/opt/jerry/media"
  # -rL dereferences symlinks so the actual file bytes land in the image,
  # not dangling symlink pointers pointing to paths that don't exist on the Pi.
  sudo cp -rL "$MEDIA_SRC/." "$MNT_ROOT/opt/jerry/media/"
  sudo chown -R root:root "$MNT_ROOT/opt/jerry/media"

  # Validate: at least one video file actually landed in the image.
  BAKED_COUNT=$(sudo find "$MNT_ROOT/opt/jerry/media" -maxdepth 1 \
    \( -iname "*.mp4" -o -iname "*.mkv" -o -iname "*.mov" \
       -o -iname "*.avi" -o -iname "*.m4v" -o -iname "*.webm" \) | wc -l)
  if [[ "$BAKED_COUNT" -eq 0 ]]; then
    err "media copy failed — no video files found in image at /opt/jerry/media.
  Check that build/media/ contains real .mp4 files (not broken symlinks)."
  fi
  log "$BAKED_COUNT video file(s) baked into image"

  # Override the media root in the service — no USB drive needed.
  sudo sed -i 's|JERRY_MEDIA_ROOT=/media/usb|JERRY_MEDIA_ROOT=/opt/jerry/media|' \
    "$MNT_ROOT/etc/systemd/system/jerry.service"
  sudo sed -i 's|ReadWritePaths=.*|ReadWritePaths=/var/lib/jerry /opt/jerry/media /tmp /run|' \
    "$MNT_ROOT/etc/systemd/system/jerry.service"
  log "media baked in — Pi will play from SD card, no USB required"
fi

# --- Enable services via symlink (no systemctl, image is offline) ---
log "enabling services in multi-user.target.wants"
sudo install -d -m 0755 "$MNT_ROOT/etc/systemd/system/multi-user.target.wants"
sudo ln -sf /etc/systemd/system/jerry.service \
  "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/jerry.service"
sudo ln -sf /etc/systemd/system/jerry-firstboot.service \
  "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/jerry-firstboot.service"

# --- Make sure NetworkManager is the network stack on Pi OS Lite ---
# Pi OS Lite Bookworm uses NetworkManager by default; older releases
# defaulted to dhcpcd. Force NM on so jerry-ap.nmconnection is honored.
if [[ -f "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/dhcpcd.service" ]]; then
  log "disabling dhcpcd (NetworkManager will own the network)"
  sudo rm -f "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/dhcpcd.service"
fi

# --- 5. Boot partition: enable SSH so the appliance can be debugged ---
log "enabling SSH on boot partition"
sudo touch "$MNT_BOOT/ssh"

# Pre-set the default `pi` user with a known password so a network-down
# Pi can still be SSHed into from a directly-connected laptop. Operator
# should change this on first login. Generate the hash fresh on each
# build so we don't ship a stale credential pinned in source control.
log "generating pi user credentials (user=pi pass=jerry)"
PI_USER_HASH="$(openssl passwd -6 jerry)"
echo "pi:${PI_USER_HASH}" | sudo tee "$MNT_BOOT/userconf.txt" >/dev/null

# --- 6. Done. Trap will unmount + detach loop. ---
log "image ready: $OUTPUT_IMG"
ls -lh "$OUTPUT_IMG"
echo
echo "Burn with Raspberry Pi Imager (recommended) or:"
echo "  sudo dd if=$OUTPUT_IMG of=/dev/sdX bs=4M conv=fsync status=progress"
echo "  (where /dev/sdX is the SD card — verify with \`lsblk\` first)"
