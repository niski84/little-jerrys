#!/usr/bin/env bash
# Idempotent runtime readiness setup. Image packages mpv/ffmpeg in advance.
set -Eeuo pipefail
exec >>/var/log/jerry-firstboot.log 2>&1
log() { printf '[%(%Y-%m-%dT%H:%M:%S)T] %s\n' -1 "$*"; }
fail() { log "ERROR: $*"; exit 1; }
trap 'rc=$?; log "ERROR: firstboot failed at line $LINENO (exit $rc)"; exit "$rc"' ERR

DONE=/var/lib/jerry/.firstboot-done
[[ -e "$DONE" ]] && exit 0
log "firstboot start"

for command in mpv ffmpeg useradd usermod getent findmnt lsblk growpart resize2fs udevadm; do
  command -v "$command" >/dev/null || fail "required command '$command' is missing"
done
mpv --version >/dev/null 2>&1 || fail "mpv failed its startup check"
ffmpeg -version >/dev/null 2>&1 || fail "ffmpeg failed its startup check"

if ! id jerry >/dev/null 2>&1; then
  log "creating jerry system account"
  useradd --system --no-create-home --shell /usr/sbin/nologin jerry
fi
for group in video audio render; do
  if getent group "$group" >/dev/null; then
    usermod -aG "$group" jerry
  else
    log "ERROR: required device group '$group' is missing"
    exit 1
  fi
done

install -d -o jerry -g jerry -m 0755 /var/lib/jerry /media/usb /opt/jerry/media
install -d -o root -g root -m 0755 /opt/jerry
[[ -x /opt/jerry/jerry ]] || fail "application binary is missing or not executable"
chown jerry:jerry /var/lib/jerry /media/usb /opt/jerry/media
chmod 1777 /tmp

# Grow the root partition before the app imports media. Pi OS firstboot still
# handles PARTUUID setup from cmdline.txt; this runs after user configuration.
ROOT_PART=$(readlink -f "$(findmnt -n -o SOURCE /)")
[[ -b "$ROOT_PART" ]] || fail "cannot identify the root block device: $ROOT_PART"
ROOT_DISK_NAME=$(lsblk -n -o PKNAME "$ROOT_PART" | head -1)
ROOT_PART_NUMBER=$(cat "/sys/class/block/$(basename "$ROOT_PART")/partition")
[[ -n "$ROOT_DISK_NAME" && "$ROOT_PART_NUMBER" =~ ^[0-9]+$ ]] || fail "cannot determine root disk and partition number"
ROOT_DISK="/dev/$ROOT_DISK_NAME"
log "growing root partition $ROOT_PART_NUMBER on $ROOT_DISK to fill the SD card"
if GROW_OUTPUT=$(growpart "$ROOT_DISK" "$ROOT_PART_NUMBER" 2>&1); then
  GROW_RC=0
else
  GROW_RC=$?
fi
if (( GROW_RC != 0 )) && [[ "$GROW_OUTPUT" != *NOCHANGE* ]]; then
  fail "growpart failed (status $GROW_RC): $GROW_OUTPUT"
fi
[[ -z "$GROW_OUTPUT" ]] || log "$GROW_OUTPUT"
resize2fs "$ROOT_PART"
udevadm settle
log "root filesystem size after growth: $(df -hP / | awk 'NR==2 {print $2}')"

# Start is deliberately local-only. Wi-Fi and recovery networking are opt-in
# through the application's own setup flow; first boot must not claim a network.
touch "$DONE"
chown root:jerry "$DONE"
chmod 0640 "$DONE"
log "firstboot ready"
