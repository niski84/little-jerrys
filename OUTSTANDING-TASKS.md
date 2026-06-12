# Outstanding Tasks

_Last updated: 2026-05-03_

Tracking all work that's been **scoped, discussed, or partially designed** but
not yet shipped. Items above the line are blocking ship. Items below the line
are nice-to-haves we explicitly deferred.

---

## Ship-blocking (action items for the operator — me, not Brian)

### 1. Push the preview image to GHCR — one-time + on each rebuild
Pipeline is wired. To actually ship, log into GHCR once and push.

```bash
echo "$GHCR_TOKEN" | podman login ghcr.io -u niski84 --password-stdin
./scripts/ship-image.sh push
```

Need a GHCR token with `write:packages` scope. Generate at
<https://github.com/settings/tokens>.

### 2. Build the Pi `.img` and ship it to Brian
Pipeline is wired. To actually produce an image:

```bash
# Download the base image (uncompressed)
curl -L -o build/pi-os-lite-arm64.img.xz \
  https://downloads.raspberrypi.com/raspios_lite_arm64_latest
xz -d build/pi-os-lite-arm64.img.xz

# Build
./scripts/cross-compile-pi.sh
./scripts/build-pi-image.sh build/pi-os-lite-arm64.img
# → build/little-jerrys-YYYYMMDD.img
```

Hand Brian the `.img` plus [`BURN-PI.md`](BURN-PI.md).

---

## Deferred (not blocking ship to Brian)

### Directory + Go module rename
The project still lives under `little-jerrys/` and the module is
`github.com/niski84/little-jerrys`. The user-facing brand is
**Little Jerry's**. Renaming is high-blast-radius (touches every
Go file's imports + any external references). Defer until after the first
Brian-approved ship — no functional impact, only cosmetic in the codebase.

### Themed feature labels
User had a table of show-derived UI strings to swap in:
- "Is anyone here a marine biologist?" → search placeholder
- "What's the deal with admin?" → login tagline
- "The Moops" → validation error helper
- (more — full table is in conversation history)

### Restaurant-locked copy scrub
`WHITEPAPER.md` and a few of the older help docs reference "Tony's
restaurant" by name. Generalize to "the venue" / "the operator" before any
public release.

### OSD splash auto-launch on captive-portal AP
We have a `/api/splash.png` endpoint that renders a 1920×1080 captive-portal
splash with a WIFI-join QR. The auto-launch behavior — mpv pre-loads the
splash on boot when AP mode is active — isn't wired yet. The image is
generated on-demand only.

### Per-episode play count UI
Play counts + completion counts are tracked per S/E key (visible on
`/summary` and on dashboard queue rows) but the **episode detail card**
itself doesn't show the per-episode stats. Add a small "Played 3× ·
Completed 2×" line next to the weight / block widgets.

### HMAC signing for outbound webhooks
Documented in `08-webhooks.md` as future work. When it lands it'll be
additive — existing receivers without HMAC validation keep working.

### Mobile usability pass
- Hamburger nav for narrow screens (< 720px the top nav wraps awkwardly)
- SortableJS touch-delay tuning so drag-to-reorder works on phone/tablet
  without conflicting with scroll
- Image `max-w` cap on episode detail card so portrait phones don't get a
  full-width still

### Long-term log persistence
The event log is an in-process ring buffer (capacity 200). Survives until
restart only. Acceptable for an appliance with stdout journaling, but a
persistent log store would help post-mortem on a power-cycle crash.

### Restaurant-locked help doc scrub
Same as the WHITEPAPER scrub — older help docs in
`internal/help/docs/01-*.md` and `02-*.md` reference Tony's by name in a
couple of paragraphs. Generalize.

---

## Done (shipped this iteration)

See conversation history. All of #24 – #41 closed:
container + VNC, broadcast UI, episode browser, playlists, weight/block
on episode detail, captive portal default, webhooks, event log strip,
queue persistence, soundboard, USB branding, project naming, live status
+ seek + preview, real play tracking, weekly summary, queue regression
tests, API reference doc, dashboard hero broadcast title-card.
