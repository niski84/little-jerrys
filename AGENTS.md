# Little Jerry's: focused working context

One active project: this checkout. The sibling `sienfeld_smash_deck` is retired
migration evidence, not a second implementation target.

Start with [the code map](docs/CODE-MAP.md), then read only the module needed.
For deployment identity/permissions, use [AGENT-HANDOFF.md](AGENT-HANDOFF.md).
For unfinished reconciliation, use [the checkpoint](docs/MIGRATION-CHECKPOINT.md).
For delegated work and remaining acceptance gates, use
[the agent work plan](docs/AGENT-WORK-PLAN.md).

- Locate symbols with `rg -n` before opening bounded sections. Exclude
  `*_templ.go`, generated CSS, media, build output and dependency directories
  unless debugging generation itself. Edit `.templ`, then regenerate.
- Start cross-repository comparisons with path/status summaries. Compare one
  subsystem at a time; normalize module-name differences before analyzing logic.
  Do not repeatedly dump whole files or combined multi-subsystem diffs.
- Use exact patch anchors from the current file. A failed patch means inspect
  the small affected section, not re-read the repository.
- Reuse the existing doc-excerpt/json-inspect skills for documentation and
  unfamiliar structured output. Do not build another helper without measured reuse.
- Keep adjacent discoveries separate unless required for this change's correctness.
  Record their verification status and next action in the checkpoint.
- Preserve the working tree. A preview/test must use temporary state/media/socket,
  stub GPIO, disabled updates and no production credentials. Never run legacy
  deployment scripts. Pi changes require the exact reviewed migration approval.
- Report which test version actually passed; later edits invalidate that evidence.
  Do not load all historical plans to resume when the checkpoint suffices.
