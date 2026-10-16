#!/usr/bin/env bash
# Mount one USB partition at /media/usb without formatting or modifying files.
set -Eeuo pipefail
DEV=${1:?usage: jerry-usb-mount.sh /dev/sdXN}
[[ "$DEV" =~ ^/dev/sd[a-z][0-9]*$ ]] || { echo "invalid USB block device: $DEV" >&2; exit 2; }
[[ -b "$DEV" ]] || exit 0
[[ "$(udevadm info --query=property --name="$DEV" | sed -n 's/^ID_BUS=//p' | head -1)" == usb ]] || exit 0

MOUNTPOINT=/media/usb
if mountpoint -q "$MOUNTPOINT"; then
  exit 0
fi
FS=$(blkid -o value -s TYPE "$DEV" || true)
[[ -n "$FS" ]] || { echo "no recognized filesystem on $DEV" >&2; exit 1; }
UID_JERRY=$(id -u jerry)
GID_JERRY=$(id -g jerry)
exec 9>/run/jerry-usb-mount.lock
flock 9
mountpoint -q "$MOUNTPOINT" && exit 0
install -d -o jerry -g jerry -m 0755 "$MOUNTPOINT"
case "$FS" in
  vfat|fat|msdos|exfat|ntfs|ntfs3|fuseblk)
    mount -o "ro,nodev,nosuid,noexec,uid=$UID_JERRY,gid=$GID_JERRY,fmask=0133,dmask=0022" "$DEV" "$MOUNTPOINT"
    ;;
  ext4)
    mount -o ro,noload,nodev,nosuid,noexec "$DEV" "$MOUNTPOINT"
    ;;
  *)
    echo "unsupported USB filesystem '$FS' on $DEV" >&2
    exit 1
    ;;
esac
