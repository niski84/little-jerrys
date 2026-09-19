# Reviewed app bootstrap — 2026-09-19

Operator approved commit/deploy after local tests. Target: canonical Little
Jerry's at 192.168.4.40, Wi-Fi MAC b8:27:eb:cc:86:85. This is the initial
legacy-to-canonical app migration, not activation of the privileged executor.

Build from the clean committed tree with Linux/ARM64, CGO disabled, `-tags pi`,
`-buildvcs=true`, and a revision-derived `main.Version`. Verify embedded revision,
`vcs.modified=false` and SHA-256 before staging over the existing SSH key.
Use `scripts/bootstrap-reviewed-pi.py` with exact binary path/digest/revision.
It deliberately refuses a changed legacy hash, identity or deployment baseline.

Changes on the Pi:

- Stop watchdog/update trigger, then the app; snapshot the quiescent binary,
  state, config, units and mpv configuration to a private versioned directory
  below `/var/backups/little-jerrys/`. Media is left in place, not copied/deleted.
- Retain `/opt/jerry/media`, `/var/lib/jerry/state.json`, current unit, credentials,
  network settings and GPIO behavior. Install only the reviewed app binary.
- Add `90-reviewed-migration.conf` and a root-only `/etc/jerry/migration.env`
  setting `JERRY_AUTO_UPDATE=false`; disable legacy `jerry-update.path`.
- Start app; verify exact clean revision/digest, device identity, real HDMI/audio
  and advancing mpv playback. Restore watchdog after successful checks.
- On failure restore the old binary/config/state and prior trigger/watchdog
  state. Keep recovery snapshots. Do not weaken maintainer provenance checks.

The separately built executor remains uninstalled/inactive. Provisioning its
secret, authenticated tunnel and Patch Hatch deployment lane is a separate
workflow; this deployment does not enable autonomous writes.
