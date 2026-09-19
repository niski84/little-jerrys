# Little Jerry's — canonical project and live appliance

Updated 2026-09-19. **One product, one active repository: Little Jerry's.**

**Current live status:** canonical app deployed from clean commit
`e4d84769081ede8e9fb67b54f6ea381a9e94dae0`; health, retained export and real
HDMI/audio/advancing playback passed. Legacy auto-update is disabled; the separate
maintainer executor remains inactive. See `docs/DEPLOYMENT-2026-09-19.md` for
exact artifact and full recovery snapshot. Pre-bootstrap evidence below is historical.

| Name | Meaning |
|---|---|
| Little Jerry's / `little-jerrys` | Canonical product, checkout and GitHub repository: `niski84/little-jerrys` |
| Channel Eleven / Channel 11 | Historical pilot/display name for the same appliance, not another repository |
| Channel Seven / Channel 14 / Seinfeld / `sienfeld_smash_deck` | Legacy names and archived predecessor; retained only for recovery and migration |

GitHub checked on 2026-09-19: `little-jerrys` is active (default branch `master`);
`sienfeld_smash_deck` is archived (default branch `main`). Do not unarchive, rename,
delete, or deploy the predecessor to resolve naming confusion. Its unique work
must be reconciled into this repository first. Do not rename the deployed Unix
account, paths, cookie or systemd unit merely to change the product name.

## Verified live target

- App: http://192.168.4.40:8089
- Hostname: `raspberrypi`; Wi-Fi MAC `b8:27:eb:cc:86:85`; UniFi VLAN 40.
- SSH: `jerry@192.168.4.40`, existing `~/.ssh/id_ed25519`, verified known-host key.
  Use BatchMode and StrictHostKeyChecking=yes. No password is needed.
- Architecture: `aarch64`; unit: `jerry.service`; executable: `/opt/jerry/jerry`.
- Source of device access records: `../infrastructure/vault.md` Device Registry.
- Do **not** use `.30` (a different/older Pi), or the stale PiKVM identity formerly
  associated with `.40`. DHCP may change; confirm MAC and application identity.

## Historical installed build before the approved bootstrap

The pre-bootstrap health response was `service:sienfeld_smash_deck`, `status:ok`, `version:dev`.
An SSH read-only check confirmed the service active and copied its binary locally
without interrupting playback. Embedded Go metadata reports:

```
main package: github.com/niski84/sienfeld_smash_deck/cmd/sienfeld_smash_deck
vcs.revision: eacdbded539c6f8269f8cd3d48dd49833a776553
vcs.modified: true
sha256:       5f8b0927aff32e1f7aa39dad410628dd000ced99a83c8a2d5910f439b937b5ba
```

Recovery snapshot for this investigation:
`build/pilot-baseline-IvvPBR/installed-jerry` (private, gitignored local copy;
SHA-256 verified against the Pi). This is a binary-only recovery copy, not a
config/state backup or a clean artifact accepted by the new executor.
The new `jerry-maintainer.service` is inactive; this is not an already-migrated
clean-revision baseline. Never bypass the executor's provenance checks to accept
this binary. A reviewed bootstrap and retained recovery copy are required.

## Preserve before consolidating

Canonical development branch: `port-sienfeld-network-safety` at `c222471` before
the uncommitted maintainer exporter/executor work. `master` is behind that branch.
Legacy local branch: `main` at `87b4fff`, with local commits beyond its archived
remote. A comparison found 102 changed paths, including unique media-root,
quiet-hours, upload, player, recovery/watchdog and updater work. This is not just
a cosmetic rename. Do not delete either checkout or blindly copy the legacy
tree over the canonical one; that would lose newer maintainer work and change
updater behavior. Media files in the predecessor are not migration source code.

## Continuing the Patch Hatch pilot

The app/bootstrap step below is now complete. Retained export works locally on
the Pi with authenticated access. Provisioning credentials/tunnel to Patch Hatch,
the separate root executor and its deployment lane is still separate work.

Use the existing portfolio entry `little-jerrys`, ID
`ccce9a56-6e20-4cea-b659-5d813399affa`; do not import the retired repository.
Read-only health observations can use the verified app URL. Do not interpret the
app deployment as approval to enable unattended deployments or change fleet gates.

1. Reconcile the unique legacy functionality into a reviewed canonical candidate;
   run unit/race, isolated browser, ARM64 build and playback/recovery checks.
2. Review the exact candidate and bootstrap migration, retain binary/config/state
   recovery copies, then approve any playback interruption separately.
3. Follow `docs/MAINTAINER-EXECUTOR.md` for clean artifacts, exporter, disabled
   legacy auto-update, root-owned actuator and authenticated tunnel provisioning.
4. Resume real-PR/human-merge/exact-deployment acceptance in
   `../patch-hatch/docs/PLAN-COMPLETION.md`. Local fixtures are not live acceptance.

Historical protocol IDs such as `channel-eleven-session` remain compatibility
identifiers; they do not represent a second product.
