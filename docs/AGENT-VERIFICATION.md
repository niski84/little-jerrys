# Lane B — local verification

Coordinator follow-up: this is the initial pre-fix evidence. Final post-fix
race/browser/vet/ARM64 success is recorded in `MIGRATION-CHECKPOINT.md`.

Date: 2026-09-19

HEAD at verification: `c222471`. The worktree was dirty before checks: 24
tracked paths modified and 24 untracked paths. Existing changes were preserved.
Results below are smoke evidence for this dirty candidate, not a clean release or
deployment artifact.

Tools and optional prerequisites:

- Go: `go1.25.0 linux/amd64` (`/usr/local/go/bin/go`)
- mpv: `v0.40.0`
- ffmpeg: `7.1.1-1ubuntu1.3`
- Both mpv and ffmpeg were available, so the prescribed real-mpv-enabled test
  command ran. No prerequisites were unavailable.

Commands and exact exit statuses (one attempt each):

| Command | Exit status | Result |
| --- | ---: | --- |
| `go vet ./...` | 0 | Passed |
| `JERRY_REAL_MPV_TEST=1 go test -race ./...` | 0 | Passed; all packages completed, including `internal/player` (6.305s) |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/little-jerrys-verification-app ./cmd/little-jerrys` | 0 | Passed |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/little-jerrys-verification-executor ./cmd/maintainer-executor` | 0 | Passed |

Build outputs were directed to `/tmp`, outside the repository. No application
server was run and no source or generated files were changed by this lane. These
checks do not establish browser, physical-device, migration, or live-service
acceptance. Next: reconcile the independent review report, address any confirmed
defect in a separately assigned change, then rerun final verification on the
resulting candidate.
