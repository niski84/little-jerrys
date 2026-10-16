# Pi 4 distributable and USB-to-SD import

Fresh image work dated 2026-10-16. This does not deploy to or change the existing
restaurant Pi. Its separate deployment/reliability acceptance remains recorded
in AGENT-HANDOFF.md and PLAYBACK-PROTECTION-DEPLOYMENT.md.

## Operator contract

The media-free image targets a Raspberry Pi 4 with a 64 GB or larger microSD card.
It contains the ARM64 app, mpv/ffmpeg and filesystem tools. First boot completes
Pi OS user setup and grows the root filesystem before starting `jerry.service`.
Ethernet provides LAN access to `http://little-jerrys.local:8089/settings`.
There is no shared SSH password, saved Wi-Fi network or enabled auto-update lane.

- USB source: `/media/usb`, mounted read-only by a systemd helper from udev.
- Local library: `/opt/jerry/media`, owned by `jerry`.
- Persistent app state: `/var/lib/jerry/state.json`.
- Last import report: `/var/lib/jerry/import-status.json`.
- Opt-in for other installations: `JERRY_IMPORT_ROOT` enables the importer;
  it must be separate from `JERRY_MEDIA_ROOT`. Empty disables it.

An importer worker scans every five seconds, serializes transfers, validates
video metadata with ffprobe, and checks copied bytes with SHA-256 before atomic
publication. A completed episode is never overwritten. Hidden staging files are
ignored by playback; interrupted ones are removed and that file starts again on
restart. Failed unchanged batches wait for a changed USB or explicit retry.
Sources and already completed episodes are retained. The importer keeps 512 MiB
free, skips source symlinks, scans subfolders, and ignores hidden/commercial/sound
folders. It copies episode videos, not arbitrary USB content or configuration.

One shared identity parser covers importer output, TMDB now-playing matching,
episode browsing, and playlist weights/blacklists. Combined files appear once in
playback; either episode resolves to that same file. Blocking either part blocks
the combined video. Browsing its second part starts the whole file; chapter offsets
and separate per-part playback statistics are not inferred.

The 180-entry catalog is already embedded. Number/title conflicts, uncertain
titles and incomplete two-part coverage are reported rather than guessed. Metadata
validation and copy verification do not prove the source video's content matches
its filename or decode every frame; the operator supplies the episode identities.

## Build and package

Use a clean committed tree. The image builder rejects a stale/dirty/non-ARM64
binary. It only modifies its output image through tracked loop mounts and refuses
to overwrite existing output. It never writes a physical card.

Dependencies: Go/npm, sudo, util-linux, parted, e2fsprogs, qemu-user-static,
Python 3, xz-utils. Use an explicit Raspberry Pi OS Lite ARM64 base. The verified
source used for this release is the Pi Foundation's 2024-11-19 Bookworm image:

```sh
curl -fL -o build/pi-os-lite-arm64.img.xz \
  https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2024-11-19/2024-11-19-raspios-bookworm-arm64-lite.img.xz
# Official compressed image SHA-256:
# 6ac3a10a1f144c7e9d1f8e568d75ca809288280a593eb6ca053e49b539f465a4
# Uncompressed SHA-256:
# 8605f56b7e725607e6bab0d0e5e52343fb5988c2172c98d034b3801efc0909a8
```

Check the official compressed hash, then decompress to `build/pi-os-lite-arm64.img`.
Runtime packages are installed from signed Bookworm repositories at build time;
playback/import require no first-boot package downloads. This is an explicit
compatibility base, not a claim that Bookworm is the latest Raspberry Pi OS.

```sh
VERSION=v0.2.0-rc.1 ./scripts/cross-compile-pi.sh
./scripts/build-pi-image.sh build/pi-os-lite-arm64.img build/little-jerrys.img
VERSION=v0.2.0-rc.1 ./scripts/package-pi-release.sh \
  build/little-jerrys.img build/pi-os-lite-arm64.img BURN-PI.md build/release
```

Packaging uses bounded two-thread xz compression (override `XZ_THREADS=1..8`),
checks the 2 GiB per-asset ceiling, and writes a flashable `.img.xz`, `BURN-PI.md`,
`build-metadata.json`, and `SHA256SUMS`. Include a runtime package manifest and
verification notes with the release. Match the Git tag to the exact clean source
revision embedded in the binary; do not tag a different default-branch commit.

## Verification

Use isolated media/state and stub GPIO/player for app/browser checks:

```sh
go test -race ./...
go vet ./...
JERRY_BROWSER_TEST=1 go test ./internal/jerry -run TestLibraryImportBrowser -count=1 -v
```

The new browser fixture blocks outside network access and exercises a real
ffmpeg/ffprobe video import, login/password change, status/checklist, retry,
system light preference, saved dark preference, and phone/desktop layout.
Do not point fixtures or workspace reloads at the live Pi. After code changes,
use the workspace reload API only for an isolated workstation instance.

Before upload, inspect the actual image for the exact clean ARM64 revision,
installed runtime versions, correct service graph, empty media/state, disabled
SSH/updater, and retained first-boot resize support. Verify the compressed stream
and SHA256SUMS. These checks do not replace flashing a spare card and testing
HDMI/audio, USB hotplug, expansion and reboot resume on a physical Pi 4. Until that
hardware test has been recorded, publish as a prerelease and state the limitation
in the release notes.
