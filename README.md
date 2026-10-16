# Little Jerry's

**This is the one active project.** Channel Eleven, Channel Seven, Channel 14
and Seinfeld are historical names for the same appliance. Start with
[the canonical handoff and verified Pi details](AGENT-HANDOFF.md) before testing
or deploying. The live Pi was migrated to the canonical app on 2026-09-19;
see [the deployment record](docs/DEPLOYMENT-2026-09-19.md).


![Little Jerry's demo](docs/little-jerrys-demo.gif)

A self-hosted Raspberry Pi appliance that plays a personal video library on
true-random rotation. Built around the Smash Deck pattern: a single Go
binary, Templ + HTMX + Tailwind 4 + DaisyUI, mpv via IPC, GPIO bailout
button.

Designed for an unattended kiosk role — coffee shop, restaurant, garage,
office lobby, kid's playroom — wherever you want a video library running
in the background and a phone/tablet to control it.

## Read this first

Depending on what you're doing:

| If you want to… | Read this |
|---|---|
| **Preview the UI on your laptop** without a Pi | [`BRIAN-PREVIEW.md`](BRIAN-PREVIEW.md) |
| **Burn a fresh SD card** for a Pi appliance | [`BURN-PI.md`](BURN-PI.md) |
| Understand the design + architecture | [`WHITEPAPER.md`](WHITEPAPER.md) |
| Browse the API | [`ENDPOINTS.md`](ENDPOINTS.md) or in-app at `/help/09-api-reference` |
| See what's still on the punch list | [`OUTSTANDING-TASKS.md`](OUTSTANDING-TASKS.md) |

## Give someone a Pi image

Download the **Pi 4 / USB import** image from
[GitHub Releases](https://github.com/niski84/little-jerrys/releases), flash it with
Raspberry Pi Imager, and insert a USB drive containing the episodes. The appliance
matches files against its bundled 180-entry Seinfeld catalog and copies them onto
the SD card. Settings shows copy progress, missing episodes, and failures. A
64 GB or larger card is required for the known ~32 GiB collection.

The image contains the app and player. Episode media is supplied separately.
Completed imports play from `/opt/jerry/media` with the USB removed; state stays
in `/var/lib/jerry`. First boot expands the root filesystem before importing.

- [Brian's download, flash, copy and repair guide](BURN-PI.md)
- [Maintainer image/release build instructions](docs/PI-RELEASE.md)

## Local dev

```bash
./scripts/compile.sh         # templ → tailwind → go build
./little-jerrys        # serves on PORT (default :8089)
./scripts/reload.sh          # restart after changes (or POST /api/reload to GoApe)
```

For Pi-parity testing in a container:
```bash
podman compose up --build    # web on :8089, VNC into mpv on :5901
```

## Stack

- **Backend:** Go 1.25 + stdlib `net/http` (no framework)
- **UI:** Templ + HTMX + Alpine.js + Tailwind 4 + DaisyUI
- **Player:** mpv (DRM/KMS direct on Pi; Xvfb in dev container)
- **State:** JSON on the SD card (`/var/lib/jerry/state.json` in release images)
- **Network:** NetworkManager via nmcli for AP fallback
- **GPIO:** periph.io with build-tag `pi`

Single binary. Embeds CSS, fonts, fan art, TMDB stills, help docs, and
soundboard slot via `go:embed`.
