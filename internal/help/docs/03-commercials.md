# Commercial breaks (broadcast programmer)

The Pi can be a 90s TV station: insert era-appropriate commercials between
episodes on a configurable schedule. Off by default — turn it on if you
want it.

## The schedule

Two knobs control how breaks fire:

- **Break every N minutes** — accumulated episode runtime that triggers a
  break. Default 22 (one break per ~22-minute episode). Set lower for more
  ads, higher for fewer.
- **Commercials per break** — how many ads run back-to-back when a break
  fires. Default 2.

Breaks happen *between* episodes, not in the middle of one. The accumulated
playtime is tracked via ffprobe at scan time.

## Per-spot weights and blocks

Every commercial in the library can be tuned individually:

- **Weight 1–10** — bias toward this spot. Weight 3 means it plays roughly
  3× as often as a default-weight spot.
- **Block** — kill a spot without removing the file. Useful when a regional
  ad gets stale or someone complains.

## Where the files live

Drop video files into the `commercials/` directory on the USB drive. They're
auto-detected on next scan. Same supported formats as episodes:
mp4, mkv, mov, avi, m4v, webm.
