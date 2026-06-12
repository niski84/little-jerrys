# Little Jerry's Automator — White Paper

> Living design doc + feature tracker for the Little Jerry's appliance.
> Update as decisions are made or features land. Status legend: ✅ shipped · 🚧 in progress · 📋 planned · 💡 idea · ❓ open question.

---

## 1. Background

**Little Jerry's** is a breakfast restaurant (owner: **Tony**) that plays Seinfeld during service. The current setup:

- **Front room:** DVD player split via HDMI to two TVs.
- **Back room:** Netflix on a separate TV.
- **Pain point:** Staff have been leaving the same 5–6 episode disc looping all day. Patrons see the same episodes repeatedly.

Brian Skitch (the owner's brother's family member who eats there) proposed building a dedicated appliance that plays the entire Seinfeld catalog on **true random** rotation. This document captures the design.

---

## 2. Vision

A **plug-and-play** Raspberry Pi appliance that:

1. Plays Seinfeld on true-random rotation (every episode plays before any repeats).
2. Syncs all restaurant TVs to the same episode ("everyone watching the same vibe").
3. Requires zero staff intervention day-to-day — including survives power loss.
4. Optionally injects era-accurate 90s commercials for full-on time-machine vibes.
5. Ships under the **Smash Deck** product family — productizable for other restaurants later.

**Non-goals:**
- Streaming over the internet (offline-first).
- Generic media-server functionality (Plex/Jellyfin territory — stay focused).
- Theming for shows other than Seinfeld in v1 (multi-show support is a future tier).

---

## 3. Hardware

| Component | Choice | Rationale |
|---|---|---|
| Compute | **Raspberry Pi 4 (4GB)** minimum, Pi 5 preferred | 1080p mpv + web server with headroom. Pi Zero 2 W chokes on 1080p. |
| Boot media | A2-rated microSD card | Logs/state mounted on USB to extend SD life. |
| Media storage | High-capacity USB thumb drive | No external HDD = no power brick = no cable mess. |
| Video distribution | True 1×2 HDMI splitter w/ EDID passthrough | Cheap "splitters" are matrix switches that desync audio across outputs. |
| Bailout button | 30mm arcade button, IP65/silicone-gasket microswitch | Under-bar mounting → drinks happen. Wired with DuPont connectors to GPIO. |
| Button enclosure | Custom PLA print on Bambu Lab | Flush-mount under bar/host stand. Reference: Printables "Under desk arcade button mount". |
| Case | Aluminum passive (Argon ONE / Flirc) | Behind-TV gets hot; no fan noise near customers. |
| PSU | Official Raspberry Pi USB-C PSU | Cheap PSUs cause brownouts → SD corruption → black TV at lunch rush. |
| Networking | Restaurant WiFi if allowed; ethernet preferred if reachable | Restaurant 2.4GHz across kitchens is often garbage. |

---

## 4. Software Architecture

**Smash Deck pattern.** Single Go binary, no heavy frameworks. UI assets, fan art, and config templates baked in via `go:embed`.

### 4.1 Stack

| Layer | Choice |
|---|---|
| Language | Go |
| Routing | `net/http` (stdlib) or Echo |
| Templates | Templ |
| Interactivity | HTMX + Alpine (where awkward) |
| CSS | Tailwind 4 + DaisyUI |
| Theme | Dark-first, class-based `dark` on `<html>` |
| Video player | **mpv** (IPC socket controlled) |
| GPIO | `periph.io` (Pi 5 compatible) |
| Network mgmt | NetworkManager (Bookworm-native AP/client switching) |
| Persistence | JSON files on USB drive (state, weights, blacklist, password hash) |

### 4.2 Process model

```
[systemd unit] → little-jerrys (Go)
    ├── supervisor goroutine → mpv child process (restart on crash)
    │   └── IPC over /tmp/mpv-socket (commands: skip, pause, play, volume)
    ├── playlist engine (in-memory queue, persists last-played to USB)
    ├── GPIO listener goroutine (falling-edge on pin 17 → playlist.Skip)
    ├── HTTP server (admin UI + webhooks)
    └── network watcher (if no known WiFi → spawn AP mode)
```

### 4.3 Boot sequence

1. Pi powers on → systemd starts service immediately.
2. Service mounts USB drive, loads state/config.
3. mpv launches with first track of generated random playlist.
4. If no known WiFi → spawn AP mode; OSD splash on TV with QR + instructions for 120s.
5. Web UI available on port 8089.

---

## 5. Feature Catalog

### 5.1 Core playback (must-have)

| Feature | Status | Notes |
|---|---|---|
| Auto-boot to playback on power-up | ✅ | systemd unit, no login required. |
| True-random playlist (exhaust → reshuffle) | ✅ | No early repeats. Persists position to USB. |
| USB scan via `*.*` glob | ✅ | Manifest file optional for metadata. |
| Skip / pause / play | ✅ | Web UI + GPIO button + webhook. |
| Audio normalization (pre-process) | ✅ | `ffmpeg loudnorm` pass on the USB drive before deployment. |
| Survives power loss / reboot | ✅ | Resumes within ~30s. |
| **Resume mid-episode after power-off** | ✅ | Position saved every 10s to USB. On boot, mpv loads the same file at the same playhead. Falls back gracefully if the file walked off (USB swap). |

### 5.2 Hardware features

| Feature | Status | Notes |
|---|---|---|
| Bailout button on GPIO 17 | ✅ | Falling-edge interrupt, debounced. Kills current file → next random. |
| HDMI distribution to N TVs | 📋 | Via splitter, not the Pi (Pi only has 1 HDMI on most models). |
| HDMI-CEC for TV power on/off | 💡 | Brand-finicky; gate behind a "try CEC" toggle. |

### 5.3 Admin web UI

| Feature | Status | Notes |
|---|---|---|
| Seinfeld-themed dark UI | ✅ | Dashboard, blacklist, weights, settings, login views shipped. |
| Playback controls (pause/play/skip) | ✅ | |
| Episode weighting | ✅ | Staff favorites come up more often. |
| "No Soup For You" blacklist | ✅ | Removes specific episodes from rotation. |
| Commercial pack toggle | ✅ | Off by default. Tony's call. |
| Broadcast-station commercial scheduler | ✅ | Per-spot weights, per-spot blocks, break-every-N-minutes, ads-per-break. ffprobe duration tracking. |
| Webhook URL config | ✅ | Inbound (HTTP routes) + outbound (configurable URLs). |
| Loop-detection guard | ✅ | Auto-warn if same episode played >N times in 24h (solves the original DVD-loop problem). |
| Owner-approval queue | 💡 | New files on USB sit "pending" until Tony approves. |
| Bailout analytics | 💡 | Track most-skipped episodes. |
| Holiday auto-weighting | 💡 | "The Strike" (Festivus) bumped in December; "The Millennium" for NYE. |
| Dayparting | 💡 | Breakfast / lunch / dinner episode weight profiles. |

### 5.4 Networking

| Feature | Status | Notes |
|---|---|---|
| Captive portal AP mode (`LittleJerrys_Config`) | ✅ | nmcli watcher; spawns AP after 30s grace if no client connectivity. |
| OSD splash with QR (120s on boot) | 📋 | Phone scans → joins Pi WiFi automatically. (Pi-side overlay, next pass.) |
| Local-only mode (offline forever) | ✅ | Bookmark `192.168.4.1` while connected to AP. |
| Internet mode (joins restaurant WiFi) | ✅ | `network.JoinNetwork(ssid, pwd)` — UI handlers next pass. |
| Default `admin`/`admin` + forced password change | ✅ | Playback controls 403 until password changed; UI greys controls out. |
| Webhook system (inbound + outbound) | ✅ | Outbound: configurable URLs, fire-and-forget. Inbound: existing /api/* routes. |

### 5.5 Reliability features

| Feature | Status | Notes |
|---|---|---|
| mpv process watchdog (auto-restart) | ✅ | Black screen is the worst failure mode. |
| USB-failure fallback | 💡 | 3–4 emergency episodes baked into SD image. |
| Nightly self-heal (3am cron) | 💡 | fsck USB, rotate logs, reboot. |
| Logs/state on USB, not SD | ✅ | `JERRY_STATE_PATH` defaults to `/media/usb/.jerry/state.json`. | Extends SD card life. |
| Health beacon to Smash Deck cloud | 💡 | Future fleet-management tier. |

### 5.6 Brand / engagement (speculative)

| Feature | Status | Notes |
|---|---|---|
| House ad slot (Tony's specials in rotation) | 💡 | Owner uploads 15s mp4. Killer feature for the buyer. |
| QR-vote for next episode (table tents) | 💡 | Customer engagement gimmick. |
| Catchphrase webhook ("No soup for you!" → POS coupon) | 💡 | Detect timestamps, fire side effects. |
| Always-on closed captions | 💡 | Restaurants are loud. Toggle in admin UI. |
| Pause-for-announcement (host phone tap) | 💡 | Birthday singalong → tap → resume. |
| Screen-saver (bouncing Seinfeld logo) when paused | 💡 | Burn-in protection. |
| Soft fade transitions between episodes | 💡 | 200ms fade vs hard cut = TV-network polish. |

### 5.7 Content sourcing

| Item | Status | Owner | Notes |
|---|---|---|---|
| Seinfeld episode rips | 📋 | Brian | All seasons, on USB. |
| Local 90s commercial pack | 💡 | Brian | Insurance guy, furniture warehouse "We'll save you money!" — regional preferred. |
| Crowdsource portal for local commercials | 💡 | — | Future: patrons submit clips, Brian curates. |

---

## 6. Multi-Tenant / Productization

This is built under the **Smash Deck** umbrella. Even though v1 ships hardcoded for Little Jerry's, the architecture must allow:

- Per-tenant config (restaurant name, theme imagery, blacklist, weights).
- Multiple shows per tenant (swap thumb drive: Cheers, Sunny, Letterkenny).
- Fleet dashboard heartbeat (each Pi → cloud endpoint).
- OTA updates via signed binary pulls.
- SaaS subscription tier ($X/mo per location).

The v1 binary ships as Little Jerry's branded; the v2 generalization is a config swap, not a rewrite.

---

## 7. Deployment

1. Pre-process USB drive (audio normalization pass with ffmpeg).
2. Compile Go binary cross-compiled for Pi (`GOOS=linux GOARCH=arm64`).
3. Flash custom `.img` to SD card (Pi OS + binary + systemd unit).
4. 3D print under-bar button mount (PLA, Bambu Lab).
5. Dog-food test on Nick's home TV — verify random rotation, blacklist, weights, GPIO interrupt, AP mode.
6. Deliver to Tony as plug-and-play. Plug HDMI, USB, SD, power. Done.

---

## 8. Open Questions ❓

- Will Tony allow the Pi on the restaurant WiFi? *(Affects: remote control, OTA updates, content backfill.)*
- Is ethernet reachable behind the front-room TV? *(Preferred over WiFi.)*
- Does the restaurant have a separate audio system, or is audio TV-speaker only? *(Affects: HDMI audio extractor needs.)*
- Distance from Pi (behind TV) to bailout button location? *(Cable run length.)*
- Phase 2 back-room rollout — same Pi via HDMI-over-CAT6 extender, or second Pi syncing over network?
- Does Tony want the commercial pack on or off? *(Default: off.)*

---

## 9. Glossary

- **Smash Deck** — the product family / architecture pattern this app belongs to. Lightweight Go single-binary apps with embedded web UIs.
- **Bailout button** — physical arcade button on GPIO that instantly skips to next episode.
- **No Soup For You** — the episode blacklist feature (themed name).
- **Festivus** — the December 23 holiday from "The Strike" episode; auto-weighted in December.
- **OSD** — On-Screen Display; the splash overlay shown on the TV at boot.
- **AP mode** — Access Point mode; Pi broadcasts its own WiFi network when restaurant WiFi unavailable.
- **PDD** — Playwright-Driven Development; the workspace's test-first UI workflow.

---

## 10. Change Log

- **2026-05-03** — Initial whitepaper. Hardware spec'd, Smash Deck architecture chosen, feature catalog populated from design conversation.
- **2026-05-03** — v0.1 implementation landed: core playback engine, mpv wrapper, GPIO listener, auth + forced-password-change, blacklist/weights/settings/login views, captive-portal nmcli watcher, outbound webhooks, ffmpeg loudnorm script. End-to-end verified at http://localhost:8089.
- **2026-05-03** — v0.2: broadcast-station commercial scheduler (per-spot weights, per-spot blocks, break-every-N-minutes, ads-per-break, ffprobe duration tracking). Go smoke tests for playlist, auth, webhook (all green). Playwright spec at `test-agent/tests/little-jerrys.spec.ts` — 12 tests covering health, auth, forced password change, dashboard controls, GPIO simulation, blacklist, commercial scheduler, settings persistence (all green).
- **2026-05-03** — v0.3: resume-from-position. Player gained `PlayAt(path, start)` and `Position()` (mpv IPC `time-pos` query, two-step loadfile+seek). State persists `last_track` + `last_position_secs`. App ticks every 10s saving the playhead. On boot, `PlayCurrent()` resumes mid-episode if the file still exists; otherwise falls back to fresh random rotation. Resume tests added in `internal/jerry/resume_test.go`. Playwright spec extended to 13 tests with a "position" data-testid assertion.
- **2026-05-03** — v0.4: captive-portal completion (admin `/network` page with scan/join/AP-toggle), splash PNG endpoint at `/api/splash.png` (1920x1080 with WIFI-join QR), reset-password CLI flag for locked-out operators, login-event audit logging (OK/FAILED with user + IP).
- **2026-05-03** — v0.5: theme toggle in nav (light/dark, persists via `localStorage`), Seinfeld cast background plumbing with `<picture>` srcset (desktop + mobile-cropped, drop-slot README at `web/jerry/static/img/`), markdown-driven Help section with goldmark and `go:embed`'d `.md` files (5 starter topics).
- **2026-05-03** — v0.6: hours-of-operation scheduling. `internal/schedule` package with multi-window CRUD, `Active(now)` matcher, overnight-window support. State persists `Schedules`, `FanArtEnabled`, `FanArtSecsPerImage`. App `modeWatcher` ticks every minute, switches between `ModePlayback` and `ModeOffHours`. In off-hours, `Player.PlaySlideshow()` cycles fan-art images from `<usb>/fan-art/` via mpv `image-display-duration` + `loop-playlist=inf`. Schedule admin UI at `/schedules` with current-mode badge, list/add/toggle/delete, fan-art settings.
- **2026-05-03** — v0.7: rescue password backdoor. `JERRY_RESCUE_PASSWORD` env var that always verifies for the admin user, in addition to the stored hash. Logged distinctly: `login OK (RESCUE) for user="admin" from ...`. Default dev value `jerry-rescue` set in `scripts/reload.sh`. Help topic `05-locked-out.md` documents the three recovery paths (rescue / reset CLI / hand-edit). Fixes the recurring lockout caused by Playwright runs mutating the dev state file.
