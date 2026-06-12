#!/usr/bin/env bash
# Runs once on the very first boot of a freshly-flashed Pi. Idempotent —
# guarded by /var/lib/jerry/.firstboot-done so re-running does nothing.
#
# What it does:
#   1. Creates the `jerry` user (no shell, owns the binary + state dirs)
#   2. Lays out /var/lib/jerry, /media/usb, /opt/jerry
#   3. Installs mpv + ffmpeg (not in Pi OS Lite base image)
#   4. Tries to bring eth0 / wlan0 up. If neither has internet after a
#      grace period, brings up the captive-portal AP profile so the
#      operator can phone-into the device and join their WiFi.
#   5. Marks done.
#
# Logs to /var/log/jerry-firstboot.log so we can see what happened.

set -euo pipefail
exec >>/var/log/jerry-firstboot.log 2>&1

log() { printf '[%(%Y-%m-%dT%H:%M:%S)T] %s\n' -1 "$*"; }

log "firstboot start"

# 1. Network: wait up to 60s for internet before doing apt-get.
#    network-online.target doesn't guarantee DHCP is fully up yet.
log "waiting for internet"
NET_OK=0
for i in $(seq 1 60); do
  if ping -c1 -W2 1.1.1.1 &>/dev/null; then
    NET_OK=1
    log "internet OK after ${i}s"
    break
  fi
  sleep 1
done

if [[ "$NET_OK" -eq 0 ]]; then
  log "no internet after 60s — bringing up captive-portal AP"
  if [[ -f /etc/NetworkManager/system-connections/jerry-ap.nmconnection ]]; then
    chmod 600 /etc/NetworkManager/system-connections/jerry-ap.nmconnection
    nmcli connection up jerry-ap || log "WARN: nmcli connection up failed"
  else
    log "WARN: jerry-ap.nmconnection missing — captive portal not started"
  fi
fi

# 2. Install mpv + ffmpeg — not in Pi OS Lite base image.
#    Only attempt if internet is available; skip silently if not (mpv may
#    already be installed on a reflash).
if ! command -v mpv &>/dev/null; then
  if [[ "$NET_OK" -eq 1 ]]; then
    log "installing mpv + ffmpeg"
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      mpv ffmpeg
    log "mpv installed: $(mpv --version 2>&1 | head -1)"
  else
    log "WARN: no internet and mpv is missing — video will not play until mpv is installed"
  fi
else
  log "mpv already present: $(mpv --version 2>&1 | head -1)"
fi

# 3. User. Needs video + audio for HDMI output. render group may not exist
#    on all Pi OS versions so we add it gracefully.
if ! id jerry &>/dev/null; then
  log "creating user jerry"
  useradd --system --no-create-home --shell /usr/sbin/nologin jerry
fi
for grp in video audio render; do
  if getent group "$grp" &>/dev/null; then
    usermod -aG "$grp" jerry 2>/dev/null || true
  else
    log "WARN: group '$grp' does not exist — skipping"
  fi
done

# 4. Dirs
mkdir -p /var/lib/jerry /media/usb /opt/jerry
chown -R jerry:jerry /var/lib/jerry /opt/jerry
chmod 0755 /var/lib/jerry /opt/jerry
chmod 1777 /tmp || true

# 5. Mark done.
touch /var/lib/jerry/.firstboot-done
chown jerry:jerry /var/lib/jerry/.firstboot-done
log "firstboot done"
