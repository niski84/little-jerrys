# Playback protection deployment — 2026-09-19

Operator approved deployment, brief disruptive recovery tests, and an overnight
read-only soak. **Original mid-file stall trigger remains unproven. Restaurant
acceptance is still blocked; recovery protection is not proof of root cause.**

## Installed artifact and recovery snapshot

- Verified target: `jerry@192.168.4.40`, Wi-Fi MAC `b8:27:eb:cc:86:85`.
- Clean app revision: `e9806357031bed1ad15f039baabf13c2d6f0d5e2`.
- Version: `protection-e9806357031b`; embedded `vcs.modified=false`.
- ARM64/Linux, `CGO_ENABLED=0`, `-tags pi -buildvcs=true -trimpath`.
- SHA-256: `97330c3322321e035e68c88e1b5e0cc4179a3ea5c3b2a668a614a8c86101d73d`.
- Installed `/opt/jerry/jerry` and running process hashes matched.
- Local artifact: `/tmp/jerry-reviewed-release-UtBzIQ/jerry` (temporary).
- Root-private Pi backup: `/var/backups/little-jerrys/20260919T230132-e9806357031b/`.
  Contains previous binary, quiescent config/state/unit archive, and manifest.
  Prior binary is clean canonical `e4d84769081ede8e9fb67b54f6ea381a9e94dae0`.
- Automatic rollback was armed during installation, not needed or fault-tested.
  A manual rollback interrupts playback: retain current state, stop watchdog/app,
  restore backup binary/config-state archive, daemon-reload and restart app plus
  previously active watchdog. It removes these playback protections.

Deployment used `scripts/deploy-reviewed-pi.py`, exact old/new digest and revision
guards, and the existing read-only physical smoke. HDMI connected, real audio,
configured video and advancing playback passed. Device ID and settings retained;
legacy updates remain disabled and privileged maintainer executor remains inactive.
No media deletion, credential changes, GitHub push or fleet deployment.

## Approved live tests

`scripts/test-live-recovery.py` is disruptive and must never run periodically.
Its root-private report is `live-recovery.json` inside the backup above.
Authentication uses only a root-local short-lived session, without exposing or
changing the existing state key.

- Owned primary mpv SIGSTOP: **passed**, autonomous recovery in 48.32 seconds.
  Readiness 503 observed, app PID unchanged, same episode and saved position.
- Intentional pause: **passed**, 40 seconds paused, no forced resume, player
  replacement or extra recovery; explicit resume restored advancement.
- Episode boundary: **passed**, seeking near EOF led to a different episode
  with advancing playback, without restarting the app.
- Test window: 2026-09-19 23:02:13–23:04:06 CDT. Final read-only physical
  HDMI/audio/advancement check also passed; app and watchdog remain active.

## Overnight monitor

`scripts/soak-playback.py` only GETs loopback health/readiness and reads mpv
properties. It does not resume, restart, seek or alter playback. Output is private
and bounded; `summary.json` updates atomically while running with `complete=false`
and `passed=false`. Interrupted or shortened runs cannot pass. New recovery
attempts, sustained unreadiness/unobservable playback, or a frozen clock fail the
run; deliberate pause/static/slideshow states are not overridden. Existing
recovery count is the baseline, not an overnight incident. A falling rolling
counter cannot hide subsequent observed increments.

Eight-hour soak is **running, outcome pending**:

- Transient unit: `jerry-playback-soak-20260919T2304.service`.
- Started: September 19, 2026 at 23:04:51 CDT.
- Expected finish: September 20, 2026 at **07:04:51 CDT** (12:04:51 UTC).
- Root-owned helper: `/opt/jerry/soak-playback-e9806357.py`.
- Private output: `/var/lib/jerry/soak-20260919T2304/summary.json` and
  `samples.jsonl` in the same directory.
- Runs as `jerry`, with no new privileges and write access limited by systemd
  to its output directory. It survives SSH/session exit but not a Pi reboot;
  a reboot/interruption is an incomplete run, never a passing result.
- Verified active and writing samples. Initial recovery baseline is 1 from the
  approved fault injection; initial recovery delta is 0, with no failures.

To review later (read-only, do not repeat the disruptive tests):

```sh
ssh -i /home/nick/.ssh/id_ed25519 -o BatchMode=yes -o StrictHostKeyChecking=yes \
  jerry@192.168.4.40 \
  'cat /var/lib/jerry/soak-20260919T2304/summary.json; systemctl show jerry-playback-soak-20260919T2304.service -p ActiveState -p Result'
```

Require `complete=true`, `passed=true`, full elapsed duration, no failures, and
inspect track transitions before describing the soak as passed. An inactive unit
alone is not evidence of success. Do not infer unattended acceptance from the
brief deployment check or the intentionally injected process failure.

Local helper verification: ten classification/lifecycle tests passed, including
stuck EOF, recovery-count expiry, missing IPC observations and interrupted runs.
Full application race/real-mpv/browser tests and vet passed for the unchanged app
code before release; see `PLAYBACK-STALL-INVESTIGATION.md`.

Sampling cannot prove physical visible/audible output or observe every transition
between samples. A human HDMI/audio check and the remaining acceptance scenarios
(responsive-stall behavior on actual hardware, finite failures, quiet hours,
repeated episode/advert boundaries) remain separate evidence requirements.
