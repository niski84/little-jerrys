# Migration agent handoff and work plan

Updated 2026-09-19. This is the dispatch entry point, not a replacement for the
portfolio design in `../../patch-hatch/docs/GOVERNED-MAINTAINER-AGENT-PLAN.md`
or its live acceptance ledger, `../../patch-hatch/docs/PLAN-COMPLETION.md`.

## Read only this starting context

Read `../AGENTS.md`, `CODE-MAP.md` and `MIGRATION-CHECKPOINT.md`. Use
`../AGENT-HANDOFF.md` only for migration/deployment identity. Existing source
changes are uncommitted and must be preserved. The retired sibling is evidence,
not a second target. Do not inherit the full conversation or scan all projects.

Objective: finish verifying the selectively reconciled Little Jerry's candidate,
then prepare a reviewed migration preserving existing appliance behavior and
the canonical exporter/executor. No new features or architecture rewrite.

## Delegation rules (initial review dispatch)

The operator subsequently approved scoped fixes and local browser verification.
The report-only restrictions below describe the first dispatch, not that approved
follow-up. Deployment/credential/commit restrictions still apply.

- Use two bounded Luna workers initially, without conversation-history forks.
- Stop after the assigned report; no automatic follow-on work or arbitrary retries.
- Read targeted source sections, exclude generated files from exploratory reads.
- No commits, pushes, PRs, reloads, production credentials, live controls, SSH,
  installations, package upgrades or deployments in these assignments.
- Do not call Patch Hatch's localhost:8100, reload API or reload script.
- Only write the assigned report. Report defects before assigning source fixes;
  playback orchestration and subprocess recovery are coupled and must not be
  edited concurrently by separate agents.
- Tests must use temporary state/media/socket, null player output or stubs, no
  updater and no physical GPIO/network mutation. Stop on unmet safety prerequisites.
- Report commands, exit status, source revision plus dirty-tree limitations,
  concrete blockers, and a concise next action. Do not equate local success with
  live acceptance. No claim of token savings without measurement.

## First dispatch: independent verification lanes

### A — migration risk review

Write only `AGENT-REVIEW.md`. Read code-map targets selectively; cap at four
actionable findings with source symbols, triggering scenario and proposed test.
Review only: shutdown/EOF/load/restart ordering, upload error classification,
media/splash configuration, and recovery Wi-Fi safety. Prior hypotheses, not
confirmed bugs: same-path stale EOF; shutdown while watcher/advance runs;
restart restoration racing a new Play; hard-coded setup text; unsupported
no-replace rename behavior. Preserve executor provenance and retained events.
Do not change implementation or repeat lane B's full test suite. If no actionable
defect is found, say so and distinguish untested physical behavior.

### B — local verification

Write only `AGENT-VERIFICATION.md`; temporary build outputs go outside the repo.
Run `go vet ./...`, `JERRY_REAL_MPV_TEST=1 go test -race ./...` when mpv/ffmpeg
are available, and Linux ARM64 builds of `./cmd/little-jerrys` and
`./cmd/maintainer-executor` with `CGO_ENABLED=0`. Use `/usr/local/go/bin/go`
if Go is absent from PATH. No dependency installation or source generation.
Record tool versions and HEAD/status summary without dumping the full diff.
One attempt per failing command; diagnose briefly, do not loop or fix code.
These dirty-tree builds are smoke artifacts, not deployable clean releases.

## Integration and remaining ordered work

- [x] Collect A/B reports and reconcile confirmed defects; assign one bounded
  source fix at a time for coupled player/app code, with regression tests.
- [x] Regenerate templates/CSS after source fixes, then isolated desktop/mobile
  browser smoke: login, quiet settings, volume, commercial uploads/campaigns,
  soundboard and authenticated retained-event access. Use disposable local data;
  do not invoke workspace reload against any live service as a preview shortcut.
- [x] Rerun affected tests and final race/vet/ARM64 builds after all edits.
- [x] Record any deferred fresh-image/firstboot work explicitly; this migration
  must preserve the installed device's paths, account, units, state, session
  compatibility and recovery Wi-Fi credentials. Do not port legacy public OTA
  or change auth-session lifetime as incidental cleanup.
- [ ] Prepare exact bootstrap diff, clean candidate provenance and rollback plan
  following `MAINTAINER-EXECUTOR.md`. Existing saved legacy binary is only a
  binary recovery copy, not a complete config/state backup or executor-approved
  clean artifact. Never weaken provenance checks to accept its dirty metadata.
  App-only bootstrap completed after operator approval; see
  `DEPLOYMENT-2026-09-19.md`. Executor artifact staging/provisioning remains pending.
- [ ] Obtain exact migration/interruption approval before any Pi mutation; retain
  binary/config/state recovery copies and disable conflicting legacy auto-update
  through the reviewed procedure.
  Approval received and app migration performed with full recovery snapshots;
  this does not approve enabling unattended fleet deployment lanes.
- [ ] Provision retained-event auth and governed executor, verify clean installed
  revision/device identity, then perform approved playback/recovery checks.
- [ ] Complete operator-selected real draft PR, human merge and exact-version
  deployment/rollback acceptance in the portfolio ledger. Do not substitute local
  fixtures or the already-passing health-only pilot for these gates.

Coordinator owns this plan and the checkpoint; workers own their two reports.
Initial dispatch status and results are recorded below, not inferred from spawn.

## Dispatch status

Two fresh-context `gpt-5.6-luna` workers started with medium reasoning:

- A: Beauvoir, `01a0bb21-0920-7662-b2f4-74319348fd9a`; review and scoped
  shutdown fix completed; focused race tests passed.
- B: Boole, `01a0bb21-094b-7093-84dd-becb023e37e2`; initial verification and
  scoped upload-classification fix completed; focused tests passed.

Coordinator removed interstitial setup claims, added regression coverage and a
repeatable loopback browser fixture, corrected mobile overflow, and regenerated
templates/CSS. Desktop/mobile browser checks and final combined race/vet/ARM64
verification passed, recorded in `MIGRATION-CHECKPOINT.md`. The approved app
commit/deploy also completed; see `DEPLOYMENT-2026-09-19.md`. Remaining portfolio
executor/PR acceptance is not complete; no workers remain active.
