# Migration checkpoint — 2026-09-19

**Current priority: playback reliability.** The deployed build later stalled
despite healthy liveness. It was manually recovered; restaurant acceptance is
blocked. New local progress/recovery/readiness changes and evidence are tracked
in `PLAYBACK-STALL-INVESTIGATION.md`. These changes are not yet deployed. The
successful bootstrap record below is historical, not unattended acceptance.

**App committed and deployed after operator approval.** Clean release
`e4d84769081ede8e9fb67b54f6ea381a9e94dae0` passed live health, retained export and
HDMI/audio/advancing playback. See [the deployment record](DEPLOYMENT-2026-09-19.md).
The following local-test evidence describes the candidate before that commit.

## Current candidate

Selective legacy reconciliation into the canonical checkout has been implemented:
quiet hours/volume, campaign uploads, now-playing queue UI, interstitials, episode
overlays, EOF handling, shutdown position saving and media-root resolution.
The canonical health/revision identity, durable event exporter, updater notice,
and separate governed executor were preserved. The legacy OTA server/installer,
longer authentication-session lifetime and immediate boot Wi-Fi takeover were
deliberately not imported. Fresh-image/firstboot tooling is not reconciled yet;
the intended live migration must preserve existing device configuration.

Additional regression fixes implemented during the port:

- Atomic no-replace uploads, including concurrent collisions/symlink collisions;
  bounded multipart memory and temporary-file cleanup.
- Serialized state persistence and deep copies of campaigns/schedule days.
- Once-only startup and stale-path/off-hours EOF rejection.
- Persistent mpv EOF observation instead of duration-based polling; explicit
  unpause on load, slideshow/episode option reset, recovery of mute/volume.
- Resume loading compatible with the local newer mpv: the real test exposed
  positional `loadfile` arguments rejected by that version; use serialized
  `options/start` plus `loadfile` instead.
- Quiet-hour mute failures retry rather than recording false success.
- Shutdown cancels/joins app workers and serializes final save/player close with
  advances; late EOF and manual advances are rejected after shutdown starts.
- Interstitial no longer advertises hardcoded recovery Wi-Fi credentials or URL.
- Sound upload distinguishes invalid input (422) from storage errors (generic
  500, privately logged), including multipart temporary-file spill failures.
- Mobile navigation/volume wrap; long soundboard paths/file inputs fit the page.

## Verified, with limits

`JERRY_REAL_MPV_TEST=1 go test -race ./...` passed after the selective port,
upload/state fixes, campaign-handler extraction and initial real-mpv test.
The real test exercised a temporary two-second video, EOF, resume, pause,
mute/volume and duplicate-EOF rejection with null outputs. The subsequent
process-crash/restart extension also passed separately with
`JERRY_REAL_MPV_TEST=1 go test -race ./internal/player -run TestRealMPVMigration -count=1`.
Initial lane B vet/race/ARM64 checks passed before the follow-up fixes. After
fixes, templates/CSS were regenerated and
`JERRY_BROWSER_TEST=1 go test ./internal/jerry -run TestMigrationBrowserSmoke -count=1 -v`
passed on desktop (1440x1000) and mobile (390x844): login/password rotation,
quiet settings persistence, volume persistence, commercial upload/campaign
weight, sound upload/catalog, event-export auth, no horizontal overflow and no
browser runtime errors. Fixture uses loopback, temporary state/media and player/
GPIO stubs; fake uploaded media tests catalog/UI, not decoding. The separate
real-mpv test covers decoding. Browser runner uses the existing sibling
`test-agent/node_modules/playwright` (override with `PLAYWRIGHT_MODULE`).
Final post-fix commands all exited 0:

- `git diff --check`
- `JERRY_REAL_MPV_TEST=1 JERRY_BROWSER_TEST=1 go test -race ./...`
  (Jerry/browser package ran in 22.179s; unchanged player results reused Go cache).
- `go vet ./...`
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build` for both
  `./cmd/little-jerrys` and `./cmd/maintainer-executor`, with outputs in
  `/tmp/little-jerrys-final-m6QePH/`.

Go executable: `/usr/local/go/bin/go`. Those tests ran on the dirty candidate
atop `c222471`; those smoke binaries were not deployed. A subsequent clean
commit was cross-built with `-tags pi -buildvcs=true` and deployed.

## Resume here — no broad rescan needed

1. App/bootstrap is complete; do not repeat the one-time script. Next portfolio
   phase is separate executor/tunnel provisioning and human-approved real PR /
   deployment acceptance. The retained legacy recovery binary still has dirty
   metadata: never stage it by weakening executor checks.

Known acceptance limits: physical recovery Wi-Fi and removable-filesystem rename
support remain untested. Fresh-image/firstboot work is deferred; this candidate
targets the existing installation. A shutdown during the interstitial may retain
the last periodic episode checkpoint when mpv reports no positive image position,
so a restart can replay near EOF. The new shutdown test verifies ordering and no
late commands, not exact physical-device resume behavior in this boundary case.

Canonical Pi identity and binary backup are in `../AGENT-HANDOFF.md`.
Full portfolio acceptance remains in the Patch Hatch completion ledger; read it
only when returning to the cross-service PR/merge/deployment phase.
