# Exact-revision maintainer executor (opt-in, not installed)

`cmd/maintainer-executor` is a separate, root-owned, loopback-only service for
Patch Hatch's governed deployment/rollback lane. It is **not** started by the app,
image builder or legacy auto-updater. The provided systemd unit is a template,
not an enabled service. No live appliance has been changed by these tests.

The app-only bootstrap was subsequently approved and deployed on 2026-09-19;
see `DEPLOYMENT-2026-09-19.md`. The executor itself remains uninstalled/inactive.

## Boundary

- Exactly one configured target/device, fixed repository `niski84/little-jerrys`.
- Fixed production destination `/opt/jerry/jerry`; fixed restart operation
  `/usr/bin/systemctl restart jerry.service`. No request-supplied commands/paths.
- Only locally pre-staged linux/arm64 Go binaries with exact clean VCS revision,
  expected main package, sealed SHA-256/size and safe ownership/modes.
- Both the new artifact **and the exact installed rollback artifact** must already
  be staged. No network download, source build, dependency install or Git hook.
- Artifacts must come from the operator's trusted build/release process. Embedded
  build metadata is an identity check, not a cryptographic supply-chain attestation.
- Root-owned private journal, target process lock, immutable operation IDs and
  one unresolved install at a time. Intents are fsynced before atomic binary swap.
- Missing response/restart means status reconciliation, never implicit replay.
  Interrupted accepted/applying intents are not restarted after executor reboot.
- A healthy running revision **and** matching installed file hash/build metadata
  are required before `installed` is reported. Patch Hatch separately observes
  the full health/event window before marking the incident done.
- Rollback needs a new explicit request matching a prior recorded deployment's
  previous revision/digest. The same install/health checks apply. No automatic
  rollback, irreversible database migration handling or multi-device rollout.

The existing release updater must be disabled (`JERRY_AUTO_UPDATE=false`) and the
app restarted with that configuration **before** this actuator accepts requests.
The health endpoint reports this setting. Never run two deployment authorities
or manually replace the binary concurrently. This actuator intentionally does
not call the legacy apply script, which has different retry/rollback semantics.

## Provisioning checklist — requires the selected Pi and explicit approval

1. Review/publish the app/exporter/executor change and build from that exact clean
   commit with `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=true`.
   Existing release generation must leave the checkout clean; dirty metadata is
   rejected. Do not forge or strip revision/dirty metadata to bypass this check.
2. Bootstrap the reviewed app/exporter on the approved Pi using its existing
   deployment procedure. Confirm `/api/health` has a clean exact revision,
   stable device ID and `auto_update:"false"`. Confirm authenticated event export.
   This initial bootstrap cannot be authorized by an executor not installed yet.
3. Review/copy the separate executor binary to
   `/usr/local/libexec/jerry-maintainer-executor`, root-owned mode 0755. Create
   `/var/lib/jerry-maintainer` root-owned mode 0700. Do not grant the `jerry` app
   user or coding/test agents write access to that directory.
4. Place trusted current/new binaries in root-owned, non-group/world-writable
   staging paths. For each exact revision run the operator-only CLI:

   ```sh
   /usr/local/libexec/jerry-maintainer-executor --stage-binary /reviewed/path/jerry-arm64 --revision EXACT_COMMIT_SHA
   ```

   It records an immutable `artifacts/COMMIT/{jerry,manifest.json}` slot and exits
   without installing/restarting. A partial/existing slot is never overwritten;
   inspect and archive it explicitly before attempting another stage.
5. Provision a unique 32+ character bearer secret in root-only
   `/etc/jerry-maintainer.env` as `JERRY_MAINTAINER_TOKEN`, together with
   `JERRY_MAINTAINER_TARGET` and the observed `JERRY_MAINTAINER_DEVICE_ID`.
   Share the secret through the existing secret manager with Patch Hatch's
   `PATCH_HATCH_DEPLOY_*` environment reference, never a prompt or source file.
6. Install/review the provided `deploy/systemd/jerry-maintainer.service` and enable
   it only with explicit operator approval. It binds `127.0.0.1:9137` by default.
   Use an authenticated tunnel from Patch Hatch; do not expose this root actuator
   on the LAN. The app health endpoint is loopback port 8089 in this adapter.
7. Configure Patch Hatch's project target, tunneled executor origin, read-only
   device/session secret reference and retained-history mode. Confirm the exact
   human merge, review deployment and interruption consent, then enable its
   separate deployment lane when ready. Test the recovery path with the exact
   recorded prior binary before relying on unattended maintenance.

## Recovery and retention

GET `/api/maintainer/operations/UUID` with the scoped bearer secret returns the
saved operation and reconciles accepted/applying state from actual installed
identity. Unknown IDs return 404, not permission to repeat a mutation. A failed
restart or mismatched binary can require direct operator recovery. No recovery
endpoint accepts arbitrary shell commands or deletes a journal entry.

The journal retains up to 10,000 operations, then fails closed until an operator
archives history. Artifact slots and previous binaries are not automatically
deleted. Watch disk space. This first adapter is for code-only changes whose old
binary is compatible with retained state; schema migrations require a different
reviewed rollback strategy.

## Local verification

`go test -race ./internal/maintainer` covers auth, repository/target constraints,
checksum tampering, process locking, duplicate and changed-ID requests, lost
ack/restart reconciliation, no interrupted replay and exact rollback. A real
clean temporary Git repository is cross-built to ARM64 and passes artifact
staging; wrong revisions, dirty builds, symlinks and unsafe permissions fail.
The state-machine tests replace only the systemd restart/build-identity boundary
with fixture callbacks/bytes. They never restart a real service or install on a Pi.
