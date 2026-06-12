# Little Jerry's


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

## Ship workflow — build a Pi image with episodes baked in

No USB drive required. Drop your episode files into `build/media/`, run two
commands, hand Brian a single `.img` he burns and boots.

```bash
# 1. Put your episode .mp4 files in build/media/ (named s01e01_title.mp4 etc.)
#    Commercials go in build/media/commercials/ (optional)
mkdir -p build/media/commercials
cp /path/to/your/episodes/*.mp4 build/media/

# 2. Download Raspberry Pi OS Lite 64-bit (one-time, ~500MB)
#    https://www.raspberrypi.com/software/operating-systems/
#    Decompress the .xz, place at build/pi-os-lite-arm64.img

# 3. Build — cross-compiles the Pi binary + bakes episodes into the image
./scripts/build-pi-image.sh build/pi-os-lite-arm64.img
# → build/little-jerrys-YYYYMMDD.img
```

Hand Brian the `.img`. He burns it with Raspberry Pi Imager, plugs HDMI into
the TV, powers on — plays immediately. No USB drive, no setup screen, no
Regenerate button. See [`BURN-PI.md`](BURN-PI.md) for Brian's full checklist.

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
- **State:** JSON on the USB drive (`/media/usb/.jerry/state.json`)
- **Network:** NetworkManager via nmcli for AP fallback
- **GPIO:** periph.io with build-tag `pi`

Single binary. Embeds CSS, fonts, fan art, TMDB stills, help docs, and
soundboard slot via `go:embed`.
