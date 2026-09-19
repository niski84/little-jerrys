# Lane A — bounded migration risk review

Coordinator follow-up: all three findings below received scoped fixes and
regression coverage. See `MIGRATION-CHECKPOINT.md` for current verification and
remaining live gates; the original findings are retained as review history.

Reviewed 2026-09-19 at `c222471`; working tree was already dirty, so findings
describe the current uncommitted candidate, not a clean release. Only this report
was written. No tests/builds were run (lane B owns verification); no production,
service, or physical-device actions were taken.

## Actionable findings

1. **P1 — shutdown can race playback work and the final position save.**
   `App.Close` cancels its context, then immediately saves position and closes
   the player (`internal/jerry/app.go:916-929`). Watcher goroutines and an
   `advanceAfterEnd` callback are not joined; the latter can hold `advanceMu`,
   play an interstitial, and load another track while shutdown samples/saves
   state or closes mpv. A restart near EOF can therefore persist a stale
   position or race player teardown. Add a shutdown gate/join for owned workers
   and in-flight advance before the final save/close; add a deterministic test
   that blocks an advance at shutdown and asserts final state and no post-close
   player command.

2. **P1 — interstitial recovery instructions ignore effective portal settings.**
   `RenderWaiting` embeds `ChannelSeven_Setup`, `channelseven`, and
   `10.42.0.1:8089` (`internal/splash/splash.go:65-67`). App startup supports a
   media-root branding config with an overridden AP SSID and configurable HTTP
   port (`internal/jerry/app.go:142-143,203-208`); the generated between-episode
   card can consequently direct staff to credentials/address that do not work.
   Pass the effective SSID/password/address/port into the renderer (or remove
   setup claims from this card); test non-default SSID and port against rendered
   text and the AP values used by the app.

3. **P2 — sound upload reports server/storage failures as client errors.**
   `/soundboard/upload` maps every `uploadFile` error to HTTP 400
   (`internal/jerry/http.go:464-468`), while that helper also returns filesystem,
   staging, write, sync, and publication failures (`internal/jerry/uploads.go`).
   On a full/read-only USB this misclassifies an appliance-side failure as a bad
   request, unlike the commercial upload path's server-error handling. Preserve
   typed error causes and map malformed/oversized/unsupported input to 4xx and
   storage/publication failures to 5xx; cover both classes in handler tests.

## Explicitly unchecked

- Physical NetworkManager/AP behavior, existing-profile credential retention,
  reconnect transitions, and whether `ip route get` is a sufficient real-world
  connectivity signal were not exercised; no Wi-Fi mutation was attempted.
- Linux no-replace rename behavior on the deployed/removable filesystem and
  cross-platform fallback semantics were not tested here. Existing code fails
  closed when publication is unsupported; confirm target-filesystem support in
  an isolated acceptance check before migration.
- No clean-tree provenance, browser, real-hardware playback, full test suite,
  vet, or ARM64 build was verified in this lane.

## Review record

Commands: targeted `rg -n`, `sed`, `nl -ba` source reads and `git rev-parse
--short HEAD`; all completed with exit status 0. HEAD: `c222471`. Existing dirty
source and untracked files were preserved. Next: coordinator should reconcile
these findings with lane B, then assign any coupled playback fix as one task;
do not infer live acceptance from this local review.
