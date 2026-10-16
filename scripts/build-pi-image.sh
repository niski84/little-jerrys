#!/usr/bin/env bash
# Build a clean, media-free Raspberry Pi OS image for a downloadable release.
# Usage: scripts/build-pi-image.sh BASE_ARM64.img [OUTPUT.img]
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

err() { echo "error: $*" >&2; exit 1; }
log() { printf '\n=== %s ===\n' "$*"; }
[[ $EUID -ne 0 ]] || err "run as a normal user; this script uses sudo for image operations"
[[ $# -ge 1 && $# -le 2 ]] || err "usage: $0 BASE_ARM64.img [OUTPUT.img]"
BASE_IMG=$(realpath "$1")
[[ -f "$BASE_IMG" ]] || err "base image not found: $BASE_IMG"
OUTPUT_IMG=${2:-"$PROJECT_DIR/build/little-jerrys.img"}
[[ "$OUTPUT_IMG" = /* ]] || OUTPUT_IMG="$PROJECT_DIR/$OUTPUT_IMG"
OUTPUT_IMG=$(realpath -m "$OUTPUT_IMG")
[[ ! -e "$OUTPUT_IMG" ]] || err "refusing to overwrite output: $OUTPUT_IMG"
[[ "$OUTPUT_IMG" != "$BASE_IMG" ]] || err "output cannot be the base image"

MEDIA_MODE=${MEDIA_MODE:-sd}
[[ "$MEDIA_MODE" == sd ]] || err "MEDIA_MODE=usb is incompatible with the read-only USB import source; use sd"
ROOT_HEADROOM_MIB=${ROOT_HEADROOM_MIB:-2048}
[[ "$ROOT_HEADROOM_MIB" =~ ^[0-9]+$ ]] || err "ROOT_HEADROOM_MIB must be an integer"
(( ROOT_HEADROOM_MIB >= 1024 && ROOT_HEADROOM_MIB <= 4096 )) || err "ROOT_HEADROOM_MIB must be 1024..4096 MiB"

for bin in losetup partprobe parted mount umount e2fsck resize2fs chroot xz sha256sum; do
  command -v "$bin" >/dev/null || err "missing required command: $bin"
done
QEMU=${QEMU_AARCH64_STATIC:-$(command -v qemu-aarch64-static || command -v qemu-aarch64 || true)}
[[ -n "$QEMU" && -x "$QEMU" ]] || err "install qemu-user-static or set QEMU_AARCH64_STATIC"
[[ -x build/jerry-arm64 ]] || err "missing rebuilt build/jerry-arm64; run scripts/cross-compile-pi.sh first"
[[ -f deploy/firstboot/jerry-usb-mount.sh ]] || err "USB mount helper missing"
command -v sfdisk >/dev/null || err "missing sfdisk (util-linux)"
GO_BIN=${GO:-$(command -v go || true)}
[[ -x "$GO_BIN" ]] || GO_BIN=/usr/local/go/bin/go
[[ -x "$GO_BIN" ]] || err "go is required to validate the ARM64 binary metadata"
CURRENT_COMMIT=$(git rev-parse HEAD)
[[ -z "$(git status --porcelain)" ]] || err "release image requires a clean source tree"
BUILD_INFO=$("$GO_BIN" version -m build/jerry-arm64) || err "cannot read build metadata from build/jerry-arm64"
BUILD_PATH=$(awk '$1 == "path" { print $2; exit }' <<<"$BUILD_INFO")
BUILD_ARCH=$(awk '$1 == "build" && $2 ~ /^GOARCH=/ { sub(/^GOARCH=/, "", $2); print $2; exit }' <<<"$BUILD_INFO")
BUILD_OS=$(awk '$1 == "build" && $2 ~ /^GOOS=/ { sub(/^GOOS=/, "", $2); print $2; exit }' <<<"$BUILD_INFO")
BUILD_TAGS=$(awk '$1 == "build" && $2 ~ /^-tags=/ { sub(/^-tags=/, "", $2); print $2; exit }' <<<"$BUILD_INFO")
BUILD_REVISION=$(awk '$1 == "build" && $2 ~ /^vcs.revision=/ { sub(/^vcs.revision=/, "", $2); print $2; exit }' <<<"$BUILD_INFO")
BUILD_MODIFIED=$(awk '$1 == "build" && $2 ~ /^vcs.modified=/ { sub(/^vcs.modified=/, "", $2); print $2; exit }' <<<"$BUILD_INFO")
[[ "$BUILD_PATH" == github.com/niski84/little-jerrys/cmd/little-jerrys ]] || err "binary was built from unexpected package path: ${BUILD_PATH:-missing}"
[[ "$BUILD_ARCH" == arm64 && "$BUILD_OS" == linux ]] || err "binary must be a linux/arm64 build (found ${BUILD_OS:-missing}/${BUILD_ARCH:-missing})"
[[ ",$BUILD_TAGS," == *,pi,* ]] || err "binary must include the pi build tag (found ${BUILD_TAGS:-missing})"
[[ "$BUILD_REVISION" == "$CURRENT_COMMIT" && "$BUILD_MODIFIED" == false ]] || \
  err "binary must be a clean build of current HEAD $CURRENT_COMMIT (found revision=${BUILD_REVISION:-missing}, modified=${BUILD_MODIFIED:-missing})"

chroot_clean() {
  sudo env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin chroot "$MNT_ROOT" "$@"
}

mkdir -p "$(dirname "$OUTPUT_IMG")"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/jerry-image.XXXXXX")
LOOP=
MNT_BOOT="$WORK/boot"
MNT_ROOT="$WORK/root"
mkdir "$MNT_BOOT" "$MNT_ROOT"
RESOLV_BACKUP=
POLICY_BACKUP=
RESOLV_CHANGED=0
POLICY_CHANGED=0
POLICY_WAS_ABSENT=0
cleanup_failed=0
cleanup_log() { printf 'cleanup: %s\n' "$*" >&2; }
restore_chroot_overrides() {
  [[ -n "$MNT_ROOT" ]] && mountpoint -q "$MNT_ROOT" || return 0
  if (( RESOLV_CHANGED )); then
    if sudo rm -f "$MNT_ROOT/etc/resolv.conf" && sudo cp -a "$RESOLV_BACKUP" "$MNT_ROOT/etc/resolv.conf"; then
      RESOLV_CHANGED=0
    else
      cleanup_log "failed to restore target /etc/resolv.conf"
      cleanup_failed=1
    fi
  fi
  if (( POLICY_CHANGED )); then
    if (( POLICY_WAS_ABSENT )); then
      if sudo rm -f "$MNT_ROOT/usr/sbin/policy-rc.d"; then
        POLICY_CHANGED=0
      else
        cleanup_log "failed to remove temporary policy-rc.d"
        cleanup_failed=1
      fi
    elif sudo rm -f "$MNT_ROOT/usr/sbin/policy-rc.d" && sudo cp -a "$POLICY_BACKUP" "$MNT_ROOT/usr/sbin/policy-rc.d"; then
      POLICY_CHANGED=0
    else
      cleanup_log "failed to restore target policy-rc.d"
      cleanup_failed=1
    fi
  fi
}
cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  set +e
  restore_chroot_overrides
  local child_mounts=("$MNT_ROOT/dev/pts" "$MNT_ROOT/dev" "$MNT_ROOT/proc")
  for target in "$MNT_BOOT" "${child_mounts[@]}"; do
    if mountpoint -q "$target" && ! sudo umount "$target"; then
      cleanup_log "could not unmount $target"
      cleanup_failed=1
    fi
  done
  local child_survives=0
  for target in "${child_mounts[@]}"; do
    if mountpoint -q "$target"; then
      cleanup_log "mount remains at $target; retaining work directory and loop device"
      child_survives=1
    fi
  done
  if (( child_survives == 0 )) && mountpoint -q "$MNT_ROOT" && ! sudo umount "$MNT_ROOT"; then
    cleanup_log "could not unmount $MNT_ROOT"
    cleanup_failed=1
  fi
  local mounts_survive=0
  for target in "$MNT_BOOT" "${child_mounts[@]}" "$MNT_ROOT"; do
    if mountpoint -q "$target"; then mounts_survive=1; fi
  done
  if (( mounts_survive == 0 )) && [[ -n "$LOOP" ]]; then
    if ! sudo losetup -d "$LOOP"; then
      cleanup_log "could not detach $LOOP; retaining work directory and image"
      cleanup_failed=1
    else
      LOOP=
    fi
  fi
  if (( mounts_survive != 0 )); then cleanup_failed=1; fi
  if (( cleanup_failed != 0 )); then
    cleanup_log "incomplete cleanup; retaining $WORK and $OUTPUT_IMG for recovery"
    (( rc != 0 )) || rc=1
    exit "$rc"
  fi
  rm -rf "$WORK"
  if (( rc != 0 )); then sudo rm -f "$OUTPUT_IMG"; fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Copy sparsely when possible. All rollback handlers are installed before the
# first loop allocation or filesystem modification.
log "copying explicit base image"
cp --reflink=auto --sparse=always "$BASE_IMG" "$OUTPUT_IMG"
truncate -s "+${ROOT_HEADROOM_MIB}M" "$OUTPUT_IMG"
LOOP=$(sudo losetup --show --find --partscan "$OUTPUT_IMG")
sudo partprobe "$LOOP"
ROOT_PART="${LOOP}p2"
BOOT_PART="${LOOP}p1"
[[ -b "$ROOT_PART" && -b "$BOOT_PART" ]] || err "expected Pi OS partitions p1 (boot) and p2 (root)"

log "growing root partition for offline runtime package installation"
sudo parted -s "$LOOP" resizepart 2 100%
sudo partprobe "$LOOP"
set +e
sudo e2fsck -f -p "$ROOT_PART"
FSCK_RC=$?
set -e
case "$FSCK_RC" in
  0|1) ;;
  2|3) log "e2fsck repaired root filesystem (status $FSCK_RC)" ;;
  *) err "e2fsck failed with status $FSCK_RC" ;;
esac
sudo resize2fs "$ROOT_PART"
sudo mount "$ROOT_PART" "$MNT_ROOT"
sudo mount "$BOOT_PART" "$MNT_BOOT"

log "preparing chroot and offline image configuration"
sudo install -m 0755 "$QEMU" "$MNT_ROOT/usr/bin/qemu-aarch64-static"
# Supply DNS only during package installation and restore the original target
# resolver object exactly on success and on cleanup after failure.
RESOLV_BACKUP="$WORK/resolv.conf"
sudo cp -a "$MNT_ROOT/etc/resolv.conf" "$RESOLV_BACKUP"
sudo cp /etc/resolv.conf "$WORK/resolv.host"
RESOLV_CHANGED=1
sudo rm -f "$MNT_ROOT/etc/resolv.conf"
sudo cp "$WORK/resolv.host" "$MNT_ROOT/etc/resolv.conf"
POLICY="$MNT_ROOT/usr/sbin/policy-rc.d"
POLICY_BACKUP="$WORK/policy-rc.d"
if sudo test -e "$POLICY" || sudo test -L "$POLICY"; then sudo cp -a "$POLICY" "$POLICY_BACKUP"; else POLICY_WAS_ABSENT=1; fi
POLICY_CHANGED=1
printf '#!/bin/sh\nexit 101\n' | sudo tee "$POLICY" >/dev/null
sudo chmod 0755 "$POLICY"
sudo mount --bind /dev "$MNT_ROOT/dev"
sudo mount --make-private "$MNT_ROOT/dev"
sudo mount --bind /proc "$MNT_ROOT/proc"
sudo mount --make-private "$MNT_ROOT/proc"
sudo mount --bind /dev/pts "$MNT_ROOT/dev/pts"
sudo mount --make-private "$MNT_ROOT/dev/pts"

log "installing offline playback runtime"
chroot_clean /usr/bin/apt-get update
chroot_clean /usr/bin/env DEBIAN_FRONTEND=noninteractive /usr/bin/apt-get install -y --no-install-recommends mpv ffmpeg exfatprogs ntfs-3g cloud-guest-utils
chroot_clean /usr/bin/apt-get clean
sudo rm -rf "$MNT_ROOT/var/lib/apt/lists"/*

restore_chroot_overrides
(( cleanup_failed == 0 )) || err "failed to restore temporary chroot configuration"
sudo rm -f "$MNT_ROOT/usr/bin/qemu-aarch64-static"

log "installing app and service configuration"
sudo rm -rf "$MNT_ROOT/var/lib/jerry" "$MNT_ROOT/opt/jerry/media" "$MNT_ROOT/media/usb"
sudo install -d -m 0755 "$MNT_ROOT/opt/jerry" "$MNT_ROOT/opt/jerry/media" "$MNT_ROOT/var/lib/jerry" "$MNT_ROOT/media/usb"
sudo install -m 0755 build/jerry-arm64 "$MNT_ROOT/opt/jerry/jerry"
sudo install -m 0755 deploy/firstboot/firstboot.sh "$MNT_ROOT/opt/jerry/firstboot.sh"
sudo install -m 0755 deploy/firstboot/jerry-usb-mount.sh "$MNT_ROOT/usr/local/sbin/jerry-usb-mount"
sudo install -m 0644 deploy/systemd/jerry.service "$MNT_ROOT/etc/systemd/system/jerry.service"
sudo install -m 0644 deploy/firstboot/jerry-firstboot.service "$MNT_ROOT/etc/systemd/system/jerry-firstboot.service"
sudo install -m 0644 'deploy/firstboot/jerry-usb-mount@.service' "$MNT_ROOT/etc/systemd/system/jerry-usb-mount@.service"
sudo install -d -m 0755 "$MNT_ROOT/etc/udev/rules.d" "$MNT_ROOT/etc/mpv"
sudo install -m 0644 deploy/firstboot/jerry-usb.rules "$MNT_ROOT/etc/udev/rules.d/99-jerry-usb.rules"
sudo install -m 0644 deploy/firstboot/mpv.conf "$MNT_ROOT/etc/mpv/mpv.conf"
sudo install -d -m 0755 "$MNT_ROOT/etc/systemd/system/multi-user.target.wants"
sudo ln -sf /etc/systemd/system/jerry.service "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/jerry.service"
sudo ln -sf /etc/systemd/system/jerry-firstboot.service "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/jerry-firstboot.service"
sudo rm -f "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/jerry-maintainer.service"
sudo rm -f "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/jerry-maintainer-executor.service"
sudo rm -f "$MNT_ROOT/etc/sudoers.d/010_pi-nopasswd" "$MNT_ROOT/etc/sudoers.d/jerry-update"
sudo rm -f "$MNT_ROOT/opt/jerry/update-apply.sh"
sudo rm -f "$MNT_ROOT/etc/systemd/system/multi-user.target.wants/ssh.service" "$MNT_ROOT/etc/systemd/system/sockets.target.wants/ssh.socket"
sudo rm -f "$MNT_ROOT/etc/ssh/ssh_host_"*

if ! chroot_clean /usr/bin/getent passwd jerry >/dev/null; then
  # Some Pi OS images already have the placeholder first user at UID 1000.
  # userconf.txt renames that account to the locked jerry account at boot.
  if ! chroot_clean /usr/bin/getent passwd 1000 >/dev/null; then
    chroot_clean /usr/sbin/useradd --system --uid 1000 --no-create-home --shell /usr/sbin/nologin jerry
  fi
fi
sudo chown -R 1000:1000 "$MNT_ROOT/var/lib/jerry" "$MNT_ROOT/opt/jerry/media" "$MNT_ROOT/media/usb"
sudo chmod 0755 "$MNT_ROOT/var/lib/jerry" "$MNT_ROOT/opt/jerry/media" "$MNT_ROOT/media/usb"

MEDIA_ROOT=/opt/jerry/media
sudo sed -i "s|^Environment=JERRY_MEDIA_ROOT=.*|Environment=JERRY_MEDIA_ROOT=$MEDIA_ROOT|" "$MNT_ROOT/etc/systemd/system/jerry.service"
# Root-owned application work will add/import from this source path.
sudo grep -q '^Environment=JERRY_IMPORT_ROOT=' "$MNT_ROOT/etc/systemd/system/jerry.service" || \
  echo 'Environment=JERRY_IMPORT_ROOT=/media/usb' | sudo tee -a "$MNT_ROOT/etc/systemd/system/jerry.service" >/dev/null
sudo sed -i 's|^ReadWritePaths=.*|ReadWritePaths=/var/lib/jerry /opt/jerry/media /tmp /run|' "$MNT_ROOT/etc/systemd/system/jerry.service"

# Stable product identity, no AP/Wi-Fi config, no inherited machine identity.
echo little-jerrys | sudo tee "$MNT_ROOT/etc/hostname" >/dev/null
sudo tee "$MNT_ROOT/etc/hosts" >/dev/null <<'HOSTS'
127.0.0.1 localhost
127.0.1.1 little-jerrys
::1 localhost ip6-localhost ip6-loopback
HOSTS
sudo rm -f "$MNT_ROOT"/etc/NetworkManager/system-connections/*
sudo rm -f "$MNT_ROOT/etc/wpa_supplicant/wpa_supplicant.conf"
sudo sh -c ": > '$MNT_ROOT/etc/machine-id'"
sudo rm -f "$MNT_ROOT/var/lib/dbus/machine-id"
sudo rm -f "$MNT_ROOT/var/lib/systemd/random-seed"
# userconf-pi creates the first OS account from this file. '*' is a locked
# password; SSH remains disabled and no shared login credential is shipped.
printf 'jerry:*\n' | sudo tee "$MNT_BOOT/userconf.txt" >/dev/null
sudo rm -f "$MNT_BOOT/ssh"

log "validating runtime and cleaning release identity"
chroot_clean /usr/bin/mpv --version >/dev/null
chroot_clean /usr/bin/ffmpeg -version >/dev/null
sudo find "$MNT_ROOT/var/log" -xdev -type f -exec truncate -s 0 -- {} +
sudo sync
log "raw image ready: $OUTPUT_IMG"
ls -lh "$OUTPUT_IMG"
echo "MEDIA_MODE=$MEDIA_MODE"
echo "Base image: $BASE_IMG"
echo "No media, Wi-Fi credentials, SSH key, or application state is included."
