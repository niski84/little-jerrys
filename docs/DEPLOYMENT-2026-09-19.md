# Little Jerry's app deployment — 2026-09-19

Operator explicitly approved commit and deployment. Completed on the verified
Pi `192.168.4.40:8089`, Wi-Fi MAC `b8:27:eb:cc:86:85`.

- App commit: `e4d84769081ede8e9fb67b54f6ea381a9e94dae0`.
- Version: `bootstrap-e4d84769081e`.
- SHA-256: `6de156ce0aa70273fbfa4830c3553cb940261c451848aec5fb7c5e50f1527c01`.
- Clean ARM64 build: `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`, `-tags pi`,
  `-buildvcs=true`, `-trimpath`; embedded `vcs.modified=false` verified locally
  and through live health. Running process and installed file digests matched.
- Local release artifacts: `/tmp/little-jerrys-release-rGgnjM/` (temporary).

## Results

- `/api/health`: canonical identity, exact revision, stable device ID
  `4ec296ebb402ad52a37d003fb9df709e`, `status=ok`, `auto_update=false`.
- Physical pre-check and two post-checks: HDMI connected, video configured,
  real audio output, unpaused playback with an advancing playhead.
- Authenticated `/api/events/export`: schema 1, durable history, events returned.
  No existing rescue password was available; the check used a root-local,
  one-minute signed session from the existing state key, never printed/persisted
  or transferred. This verifies export/session auth, not password-entry login.
- Existing password/session/volume/campaign settings retained. Empty quiet-hour
  strings normalized to 22:00/07:00; quiet mode remains disabled.
- App and watchdog active. Legacy `jerry-update.path` disabled/inactive and
  `jerry-update.service` inactive. No startup failure indicators in the inspected
  post-deployment journal window.
- Media, device accounts, network configuration and existing unit preserved.
  No media deletions, password changes or GitHub pushes.

## Recovery

Root-private backup on Pi:
`/var/backups/little-jerrys/20260919T154145-e4d84769081e/`.
Contains `jerry` (old binary), `config-state.tar` (quiescent state/config/units/mpv
config), and `manifest.json` (digests and previous unit states).
Media remains unchanged at `/opt/jerry/media` and is not duplicated in this backup.

New files: `/etc/systemd/system/jerry.service.d/90-reviewed-migration.conf` and
root-only `/etc/jerry/migration.env`. They disable the app's old updater.
Do not blindly replay the bootstrap script: its legacy hash guard intentionally
rejects the migrated appliance. For deliberate recovery, stop watchdog/app,
retain current state first, restore the exact old binary and archived config/state,
remove only these two migration additions, reload systemd and restore the recorded
unit states. This causes a playback interruption and needs explicit approval.
Automatic recovery was armed during this deployment but was not needed or tested
as a disruptive live rollback drill.

The privileged executor was built but **not installed/enabled**. Patch Hatch's
executor secret/tunnel, artifact staging, real-PR/human-merge workflow and
unattended deployment gates remain separate. Physical Wi-Fi failover and a full
episode-boundary/rollback acceptance run are not claimed by these smoke checks.
