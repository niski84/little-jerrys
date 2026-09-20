# Code map: read by task, not by repository

This is a routing index, not a copy of source. Find current line numbers with
`rg -n 'SYMBOL' PATH`. Open the named file only when that task needs it.

| Task | Source / entry points | Verification |
|---|---|---|
| Boot and updater activation | `cmd/little-jerrys/main.go`, `internal/jerry/config.go` | app tests; ARM64 build |
| Playback orchestration, quiet hours, shutdown | `internal/jerry/app.go`: `StartCurrentMode`, `advanceAfterEnd`, `applyQuietMode`, `Close` | `internal/jerry/migration_test.go` |
| mpv commands, EOF, resume, subprocess recovery | `internal/player/player.go`; `stub.go` | `internal/player/migration_test.go`; opt-in real mpv |
| Frozen clock / bounded recovery / readiness | `internal/player/progress.go`; `internal/jerry/playback_health.go` | `progress_test.go`, `responsive_stall_test.go`, opt-in `stall_integration_test.go`; `docs/PLAYBACK-STALL-INVESTIGATION.md` |
| Approved Pi recovery tests / read-only overnight soak | `scripts/test-live-recovery.py` (disruptive); `scripts/soak-playback.py` (read-only) | `scripts/test_soak_playback.py`; `docs/PLAYBACK-PROTECTION-DEPLOYMENT.md` |
| Persistent settings / migration | `internal/jerry/state.go`: `NewStore`, `Get`, `Update` | `state_test.go`, `migration_test.go` |
| Routes, health identity, event export | `internal/jerry/http.go`: `NewServer` | `events_http_test.go`, `auth_http_test.go` |
| Commercial campaigns | `internal/jerry/campaign_http.go`; `effectiveCommercialSettings` in `app.go` | `uploads_e2e_test.go` |
| Upload limits / atomic publication | `internal/jerry/uploads.go`, `upload_publish_{linux,other}.go` | `uploads_test.go` |
| Playlist / weights | `internal/playlist/playlist.go` | `playlist_test.go` |
| Recovery Wi-Fi safety | `internal/network/network.go`: `Watcher`, `HasInternet`, `EnsureAPProfile` | `migration_test.go`; physical changes separately approved |
| Media selection / splash | `internal/mediaroot/`, `internal/splash/splash.go` | media-root tests; preview |
| Dashboard, settings, soundboard | `internal/jerry/views/*.templ`, `web/styles/input.css` | generated templates + browser smoke |
| Durable journal | `internal/eventlog/{eventlog,journal}.go` | journal tests + authenticated exporter tests |
| Governed deployment actuator | `internal/maintainer/`, `cmd/maintainer-executor/` | `docs/MAINTAINER-EXECUTOR.md`, maintainer tests |

## Existing commands

Run in this checkout; none of these commands deploy to a Pi.

```sh
go test ./internal/jerry ./internal/player
go test -race ./...
go vet ./...
JERRY_REAL_MPV_TEST=1 go test -race ./internal/player -run TestRealMPVMigration -count=1
JERRY_BROWSER_TEST=1 go test ./internal/jerry -run TestMigrationBrowserSmoke -count=1 -v
JERRY_STALL_TEST=1 go test -race ./internal/player -run TestRealMPVRecoversUnresponsiveProcess -count=1 -v
go run github.com/a-h/templ/cmd/templ generate -path internal/jerry/views
npm run build:css
```

Real-mpv tests require local `mpv` and `ffmpeg`; they use temporary media/socket
and null audio/video, not the physical display. Generated templates are build
outputs, not an alternate source of application logic. Production compilation
requires clean revision metadata; a successful dirty build is only a smoke test.

Browser smoke uses Node and the existing sibling test-agent Playwright install
(override `PLAYWRIGHT_MODULE` if needed). It starts its own loopback-only fixture
with temporary state/media and stubbed playback/GPIO; do not point it at a kiosk.

## Boundaries not to rediscover

- `little-jerrys` is canonical; legacy channel names remain compatibility aliases.
- Keep the current retained-event exporter and separate maintainer executor.
- Do not port the legacy public OTA hub or its automatic installer wholesale.
- Keep the opt-in and hardware safeguards around recovery Wi-Fi.
- Do not rename deployed paths, Unix user, cookie, AP credentials or units as
  part of code branding. Deployment compatibility is separate from display names.
