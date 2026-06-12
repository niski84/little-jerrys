# Little Jerry's — Preview Setup

Hey Brian — this lets you click around the actual appliance UI on your
workstation before I burn it onto a Pi and ship it. No source code, no Go
toolchain. Just podman + a few `.mp4` files.

## What you need

- **Podman** (or Docker — both work)
- A handful of test episode `.mp4` files (any video files actually; doesn't
  have to be Seinfeld for this preview round)
- A VNC client if you want to see what's rendering — TigerVNC, RealVNC,
  Mac built-in viewer (`open vnc://localhost:5901`), all fine

## One-time setup

```bash
# Pick any folder you like
mkdir -p ~/little-jerrys-preview && cd ~/little-jerrys-preview

# Grab the compose file
curl -O https://raw.githubusercontent.com/niski84/little-jerrys/main/compose.brian.yaml

# Make a folder for episodes
mkdir -p media/commercials

# Copy your episode .mp4 files into ./media/ — see naming below.
```

## Naming your video files

The player identifies episodes by filename. Name them like this:

```
media/
  s01e01_the_seinfeld_chronicles.mp4
  s01e02_the_stake_out.mp4
  s01e03_the_robbery.mp4
  s02e01_the_ex_girlfriend.mp4
  ...
  commercials/
    ad_pepsi.mp4
    ad_whatever.mp4
```

Rules:
- Episodes go in the **root** of `media/` — name them `sSSe EE_anything.mp4` (season + episode number, then anything you want after the underscore)
- Commercials go in `media/commercials/` — any filename is fine
- Supported formats: `.mp4` `.mkv` `.mov` `.avi` `.m4v` `.webm`
- The name after the underscore is just for your own reference — the player only cares about the `s01e01` part

## Run it

```bash
podman compose -f compose.brian.yaml up -d
```

First run takes ~30 seconds while it pulls the image. After that:

- **Web UI:** http://localhost:8089
- **What's on the TV:** vnc://localhost:5901 (no password)

Default login: **admin / admin** — it'll force you to change it on first
login. Pick anything; the rescue password is `jerry-rescue` if you ever
lock yourself out.

## What to click first

1. **Dashboard** — there's a now-playing bar with a video preview, a
   progress slider you can scrub, and a queue. Click _Regenerate_ if the
   queue is empty.
2. **Episodes** — season tabs along the top. Click any episode to see the
   detail card with weight (1–10) and block toggle. Higher weight = plays
   more often.
3. **Playlists** — make a custom playlist, drag episodes into it, hit
   play. Replaces the current queue.
4. **No Soup For You (Blacklist)** — episodes you've blocked.
5. **Soundboard** — drop `.mp3` / `.wav` / `.ogg` into `./media/sounds/`
   on your host (we'll need to make that), then click _Rescan_.
6. **Summary** — weekly + all-time playback stats. Empty until you watch
   stuff for real.
7. **Settings → Webhooks** — outbound webhook URLs for Home Assistant /
   Slack / whatever. Optional.

## Stop it

```bash
podman compose -f compose.brian.yaml down
```

To wipe settings and start fresh (loses your password change, weights,
playlists):

```bash
podman compose -f compose.brian.yaml down -v
```

## Pull the latest

I'll be pushing updates as I make changes. To grab the latest:

```bash
podman compose -f compose.brian.yaml pull
podman compose -f compose.brian.yaml up -d
```

## Things that won't work in this preview

- **GPIO bailout button** — there's no Pi. The web UI has a fake-press
  button on the dashboard for testing.
- **Captive-portal AP fallback** — the appliance switches to "WIFI hotspot
  for setup" mode when no LAN is available. Containers don't have
  NetworkManager so this page just says "unavailable" — that's expected.
- **HDMI-1 slideshow** — needs two monitors on a real Pi. The slideshow
  tab works, the chromium-kiosk on a second display doesn't.

Everything else — the playback engine, queue, playlists, weights,
soundboard, scheduling, weekly summary, event log, video preview, status
API — should all be live.

## Found a bug?

Click the live event-log strip at the bottom of any page → _copy log_ →
paste it back to me. That captures the last 200 events with timestamps.
Helps me figure out what blew up without you needing to open a terminal.
