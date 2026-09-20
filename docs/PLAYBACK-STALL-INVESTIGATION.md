# Restaurant playback stall — investigation, 2026-09-19

**Not production-accepted. Underlying stall trigger is not yet established.**
Release `e4d8476` passed brief physical smoke but later stopped advancing inside
an episode. This invalidates any inference that brief health/playback checks
establish unattended reliability. Do not claim that auto-recovery proves root cause.

## Established evidence

- Pi and mpv IPC remained responsive. Playhead stayed at 465.006s of 1366.699s;
  `pause=false`, `idle-active=false`, `eof-reached=false`, `core-idle=false`,
  `paused-for-cache=false`, `seeking=false`. API Resume returned 204 without
  advancing the playhead. This was not a deliberate pause or episode EOF.
- Reloading the **same** file at the same position restored 466.675 → 469.678s,
  without rebooting the Pi or restarting the app. A subsequent physical smoke
  again confirmed HDMI/audio/progress. This is recovery, not diagnosis.
- Active media: H.264 1280x720, 23.976fps, AAC stereo 48kHz; local filesystem,
  mpv 0.35.1, ALSA HDMI. At later capture, `hwdec-current=no`, no decoder/frame
  drops, available RAM ~575MB. No evidence supports blaming hardware decoding.
- The entire affected 168,558,354-byte episode was copied to workstation temp
  storage and decoded through FFmpeg with video/audio selected. Exit 0, no
  warnings. This reduces suspicion of corrupt media but does not test Pi drivers.
- Pi temperature was 60.1°C and `get_throttled=0x80008`: soft temperature limit
  active/occurred, not evidence of undervoltage or hard throttling. This is a
  potential operating-condition concern, not an established cause.
- Historical kernel `mmc1` messages predated this deployment; not correlated
  with the stall, and controller identity must not be assumed to be media storage.
- A non-disruptive 60-second IPC capture after recovery sampled 828.828 →
  888.888s in 13 samples. Audio timestamps advanced too, AV sync stayed near
  zero, pause/cache/EOF stayed false, and the subscribed mpv warning stream was
  empty. The original transient failure did not recur during this observation.

## Confirmed detection and evidence gaps

- Existing external watchdog only checks HTTP response, service and mpv PID.
  A responsive player with a frozen clock passes all three checks.
- `/api/health` unconditionally reported `status=ok`; it was service liveness,
  not sustained playback readiness.
- mpv child stdout/stderr are discarded; app stdout/stderr point at `/dev/tty1`.
  The earlier deployment journal check saw service lifecycle messages only.
  Absence of journal errors **did not** establish absence of mpv errors.
- Production startup could silently substitute a development stub on mpv failure.
- The full local suite exposed a separate first-file/EOF race: New could return
  before the event connection was established. If `file-loaded` was missed,
  `endSeen` could remain true and suppress EOF advancement. The new spawn path
  waits for the event subscription to be ready before accepting playback loads.
  This explains a possible episode-boundary freeze, **not** the observed mid-file
  stall (which explicitly reported EOF false).

## Local protection candidate

Implemented progress detection/recovery and retained mpv warning/stall evidence.
Samples every 5 seconds, considers 30 seconds of sustained non-progress a stall,
allows finite buffering grace (120 seconds), first reloads the same track/position
then escalates to restarting the owned mpv process. Hard rolling budget: at most
3 recovery attempts per 10 minutes; budget exhaustion latches visible failure
until a new playback intent. Brief movement does not erase the attempt budget.
Manual intent/path/shutdown guards prevent stale recovery overriding controls.
Readiness waits for measured advancement, not a successful command response.
Explicit development stubs remain opt-in; production fails visibly when mpv
cannot start. mpv warning/error events are bounded and retained in the existing
authenticated journal (not parsed to trigger recovery).

HTTP contract: `/api/health` remains HTTP 200 for process liveness and adds
`liveness`, `playback_state`, `playback_ready`; its JSON `status` is `degraded`
when playback is not ready. `/api/ready` returns 503 for unverified/starting/
stalled/recovering/failed playback, using cached observations (no HTTP-path IPC).
Deliberate pause/static/slideshow modes are expected states, not reasons to
override an operator. The old PID/HTTP watchdog remains liveness-only; do not
point it at readiness and accidentally reset the bounded recovery budget by
restarting the whole app in an endless loop.

The operator subsequently approved deployment and controlled interruptions.
Clean protection release `e9806357` is now installed; see
`PLAYBACK-PROTECTION-DEPLOYMENT.md` for live results and the pending soak outcome.
Keep restaurant release blocked until the remaining acceptance evidence exists.

## Verification

Focused race tests passed for pause/slideshow/static/EOF/seeking guards, buffering
grace, rolling budget, query timeout/cancellation, and health snapshots. A fake
IPC regression reproduces the observed responsive frozen clock at 465.006s and
verifies same-track reload, actual movement before readiness, stale-intent and
post-close rejection. Production-mpv failure and readiness HTTP tests passed.

Opt-in `TestRealMPVRecoversUnresponsiveProcess` passed in 51.37s: it generated a
private clip, ran mpv with null audio/video, sent SIGSTOP only to its own child,
and observed autonomous restart plus advancing playback. This is a real process
fault test on the workstation, **not** a reproduction of the original Pi driver
stall and not a physical HDMI/audio fault test. No injected live faults.

The first combined run failed the real EOF test; after the event-startup ordering
fix, `JERRY_REAL_MPV_TEST=1 go test -race ./internal/player -run TestRealMPVMigration -count=3 -v`
passed three consecutive real-mpv EOF/resume/process-restart cycles.

Final post-fix verification all passed (exit 0):

- `JERRY_STALL_TEST=1 JERRY_REAL_MPV_TEST=1 JERRY_BROWSER_TEST=1 go test -race ./... -count=1`;
  player package 53.041s, Jerry/browser package 25.460s, no cached results.
- `go vet ./...` and `git diff --check`.
- `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags pi`;
  local smoke binary `/tmp/jerry-stall-build-0DrmA8/jerry` (dirty, not a release).
- Final read-only live smoke: existing deployed build still had HDMI/audio and
  advancing playback. No new live binary or service configuration was installed.

The following describes the pre-deployment verification. Changes were subsequently
committed and the approved deployment/live testing window began; current results
and rollback provenance are recorded in `PLAYBACK-PROTECTION-DEPLOYMENT.md`.

Before restaurant acceptance: demonstrate stuck-but-responsive recovery,
unresponsive-process recovery, no interference with deliberate pause/off-hours,
finite retry behavior and visible failure; verify repeated real episode/advert
boundaries and a long physical HDMI/audio soak. Controlled live fault injection
and a new deployment require an approved window. No such faults were injected
during the initial diagnostic phase; the subsequently approved tests are recorded
separately in the deployment report.

Sources for interpretation: [Raspberry Pi throttle bits](https://www.raspberrypi.com/documentation/computers/os.html#get_throttled)
and [mpv JSON IPC log/event protocol](https://mpv.io/manual/stable/#json-ipc).
Diagnostic log text is evidence only, never a parser-driven recovery trigger.
