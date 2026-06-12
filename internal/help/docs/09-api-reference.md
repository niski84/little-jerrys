# API Reference (advanced)

> Advanced — for developers writing scripts, dashboards, or
> automations that talk to the appliance. The web UI uses these same
> endpoints internally.

All requests are HTTP/1.1. Auth-required endpoints expect a session
cookie obtained from `POST /login`. All paths are relative to
`http://<appliance>/`.

## Auth column legend

- **Public** — no cookie required (kiosk pages, static assets, splash)
- **Auth** — session cookie required (any user-facing route)
- **Auth + Unlocked** — auth + the operator must have changed the
  default `admin/admin` password. Playback / mutation endpoints fall
  here so a fresh-install appliance can't be controlled before
  setup.

---

## Authentication

| Method | Path | Auth | Body / params | Notes |
|---|---|---|---|---|
| `GET` | `/login` | Public | — | HTML form |
| `POST` | `/login` | Public | `user`, `password` (form) | 303 → `/` (or `/change-password` on first login). Logged as `login OK` / `login FAILED` / `login OK (RESCUE)`. |
| `POST` | `/logout` | Auth | — | 303 → `/login` |
| `GET` | `/change-password` | Auth | — | HTML form |
| `POST` | `/change-password` | Auth | `password`, `confirm` | Min 8 chars, can't reuse `admin`. 303 → `/` on success. |

## Status & introspection

| Method | Path | Auth | Notes |
|---|---|---|---|
| `GET` | `/api/health` | Public | `{status:"ok", service}` |
| `GET` | `/api/status` | Auth | Live state JSON: `now_playing`, `position_secs`, `duration_secs`, `paused`, `queue_remaining`, `upcoming[8]`, `mode`, `season`, `episode`, `now_title` |
| `GET` | `/api/events/recent?n=N` | Auth | Last N events from the in-process ring (default 20, max 200). Newest first. |
| `GET` | `/api/now/preview` | Auth | Streams the currently-playing media file with range support. `<video>` tags can scrub against it. |
| `GET` | `/api/splash.png` | Public | 1920×1080 captive-portal splash with WIFI-join QR |

## Playback control

| Method | Path | Auth | Body | Notes |
|---|---|---|---|---|
| `POST` | `/api/play` | Auth + Unlocked | — | Resume |
| `POST` | `/api/pause` | Auth + Unlocked | — | Pause |
| `POST` | `/api/skip` | Auth + Unlocked | — | Advance to next item; fires bumper wipe + outbound webhook |
| `POST` | `/api/seek` | Auth + Unlocked | `secs=<float>` (form or query) | Jump to absolute position |
| `POST` | `/api/button/press` | Auth + Unlocked | — | Simulates a GPIO bailout press (dev/testing) |

## Queue

| Method | Path | Auth | Body | Notes |
|---|---|---|---|---|
| `POST` | `/api/queue/regenerate` | Auth + Unlocked | — | Fresh shuffle with current weights/blacklist |
| `POST` | `/api/queue/rescan` | Auth + Unlocked | — | Re-read the USB drive; useful after a hot-swap |
| `POST` | `/api/queue/remove` | Auth + Unlocked | `index=<int>` | Drop one item from the queue |
| `POST` | `/api/queue/reorder` | Auth + Unlocked | `from=<int>`, `to=<int>` | Move an item |

## Episodes (browse + per-episode controls)

| Method | Path | Auth | Notes |
|---|---|---|---|
| `GET` | `/episodes` | Auth | Default to season 1 |
| `GET` | `/episodes/{season}` | Auth | Season's episode list |
| `GET` | `/episodes/{season}/{episode}` | Auth | Detail panel + weight + block + add-to-playlist widgets |
| `POST` | `/episodes/{season}/{episode}/weight` | Auth + Unlocked | `weight=<int>` (1–10). 303 → episode detail. |
| `POST` | `/episodes/{season}/{episode}/blocked` | Auth + Unlocked | Toggle block. Redirects back to caller (episode detail or `/blacklist`). |

## Blacklist

| Method | Path | Auth | Notes |
|---|---|---|---|
| `GET` | `/blacklist` | Auth | Bulk view, collapsible per season. Toggle posts route through `/episodes/{S}/{E}/blocked`. |

## Playlists

| Method | Path | Auth | Body | Notes |
|---|---|---|---|---|
| `GET` | `/playlists` | Auth | — | List + create form |
| `POST` | `/playlists/create` | Auth + Unlocked | `name` | 303 → `/playlists` |
| `POST` | `/playlists/{id}/delete` | Auth + Unlocked | — | 303 → `/playlists` |
| `POST` | `/playlists/{id}/play` | Auth + Unlocked | — | Loads playlist into queue, advances to first item, 303 → `/` |
| `POST` | `/playlists/{id}/remove` | Auth + Unlocked | `season`, `episode` | Remove one episode from the playlist |
| `POST` | `/playlists/add-episode` | Auth + Unlocked | `playlist_id`, `season`, `episode` | Used by the "Add to playlist" widget |

## Schedules (hours of operation + fan-art + slideshow)

| Method | Path | Auth | Body | Notes |
|---|---|---|---|---|
| `GET` | `/schedules` | Auth | — | All schedules + fan-art + HDMI-1 slideshow settings |
| `POST` | `/schedules/add` | Auth + Unlocked | `name`, `start_time` (HH:MM), `end_time`, `days[]` (0=Sun..6=Sat) | Add a window |
| `POST` | `/schedules/toggle` | Auth + Unlocked | `id` | Enable/disable a schedule |
| `POST` | `/schedules/delete` | Auth + Unlocked | `id` | Remove a schedule |
| `POST` | `/schedules/fan-art` | Auth + Unlocked | `enabled`, `secs_per` | Save fan-art slideshow settings |
| `POST` | `/schedules/slideshow` | Auth + Unlocked | `enabled`, `layout` (1/4/16), `secs_per` | Save HDMI-1 slideshow settings |

## Settings + commercials

| Method | Path | Auth | Body | Notes |
|---|---|---|---|---|
| `GET` | `/settings` | Auth | — | Loop-guard threshold + outbound webhook URLs |
| `POST` | `/settings/save` | Auth + Unlocked | `loop_threshold`, `webhook_outbound` (newline-separated) | |
| `GET` | `/commercials` | Auth | — | Break scheduler + per-spot library |
| `POST` | `/commercials/schedule` | Auth + Unlocked | `enabled`, `break_after`, `per_break` | |
| `POST` | `/commercials/library` | Auth + Unlocked | `cw:<path>=N`, `cb:<path>=1` | Per-spot weights + blocks |

## Network

| Method | Path | Auth | Body | Notes |
|---|---|---|---|---|
| `GET` | `/network` | Auth | — | Connectivity status + scan + AP controls. Shows live LAN IPs + AP address. |
| `POST` | `/network/scan` | Auth + Unlocked | — | Force a WiFi rescan |
| `POST` | `/network/join` | Auth + Unlocked | `ssid`, `password` | Join a WiFi network; brings AP down on success |
| `POST` | `/network/ap/start` | Auth + Unlocked | — | Start the captive-portal AP |
| `POST` | `/network/ap/stop` | Auth + Unlocked | — | Stop the captive-portal AP |

## Slideshow (HDMI-1 kiosk)

| Method | Path | Auth | Notes |
|---|---|---|---|
| `GET` | `/slideshow` | Public | Kiosk page loaded by chromium on the Pi's HDMI-1 |
| `GET` | `/api/slideshow/catalog` | Public | JSON list of clip-art image URLs |

## Soundboard

| Method | Path | Auth | Notes |
|---|---|---|---|
| `GET` | `/soundboard` | Auth | Arcade button grid; one button per audio file in `<MediaRoot>/sounds/` |
| `GET` | `/sounds/{filename}` | Public | Serves a runtime soundboard clip |

## Help + summary

| Method | Path | Auth | Notes |
|---|---|---|---|
| `GET` | `/help` | Auth | Topic index (lands on first topic) |
| `GET` | `/help/{slug}` | Auth | One topic; markdown rendered server-side via goldmark |
| `GET` | `/summary` | Auth | This-week + last-week + all-time playback rollup |

## Static assets

| Method | Path | Auth | Notes |
|---|---|---|---|
| `GET` | `/static/*` | Public | Bundled CSS, fonts, images, slideshow clip art |
| `GET` | `/tmdb/*` | Public | Bundled TMDB stills (build-time prefetch) |
| `GET` | `/branding/*` | Public | Operator-provided logo / branding overrides from `<MediaRoot>/branding/` |

## Scripting recipe

```sh
# Login + capture cookie
JAR=/tmp/jerry.cookies
curl -sS -c $JAR -d "user=admin&password=YOUR_PW" http://10.0.0.50/login >/dev/null

# What's playing
curl -sS -b $JAR http://10.0.0.50/api/status | jq '{title: .now_title, pos: .position_secs, dur: .duration_secs, paused, mode}'

# Skip + seek + pause
curl -sS -b $JAR -X POST http://10.0.0.50/api/skip
curl -sS -b $JAR -d "secs=120" -X POST http://10.0.0.50/api/seek
curl -sS -b $JAR -X POST http://10.0.0.50/api/pause

# Tail recent events
curl -sS -b $JAR 'http://10.0.0.50/api/events/recent?n=50' | jq '.events[] | "\(.time) [\(.level)] [\(.source)] \(.message)"'
```

## Sessions, rate limits, future-proofing

- Session cookies last 12 hours. Re-login when they expire (long-running scripts can re-auth on 401).
- No rate limiting today. The appliance is single-user / single-tenant; no protection against abuse from a hostile LAN. Don't expose it to the public internet without a reverse proxy in front.
- Endpoints with `Auth + Unlocked` return `403 forbidden` when the
  password is still default. Check by calling `/api/status` first
  — it works on default-password installs.
- HMAC signing for outbound webhooks isn't implemented yet. When
  it lands it'll be additive (existing receivers keep working).
