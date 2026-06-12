# Webhooks (advanced)

> Advanced — useful for home automation, fleet dashboards, or signage triggers.
> Not needed for normal operation.

The appliance speaks webhooks in **both directions**:

- **Outbound** — when something happens (episode start, button press,
  schedule change), we POST a JSON event to URLs you configure.
- **Inbound** — your other systems can trigger playback by hitting the
  appliance's `/api/*` endpoints.

## Outbound: configure URLs

Settings → **Outbound webhooks** — one URL per line. Save.

Every event POSTs the same envelope. Receivers can switch on `type`:

```json
{
  "type": "episode_start",
  "path": "/media/usb/s02e11_chinese_restaurant.mp4",
  "timestamp": "2026-05-03T18:42:09-05:00",
  "season": 2,
  "episode": 11,
  "title": "The Chinese Restaurant",
  "synopsis": "...",
  "source": ""
}
```

`season`/`episode`/`title`/`synopsis` populate when the filename parses to a
known TMDB reference (`sNNeNN_*`). Otherwise they're omitted and you only
get `type` + `path`.

### Event types

| `type` | When it fires | Extra fields |
|---|---|---|
| `episode_start` | A new episode begins playing | season, episode, title, synopsis (when TMDB resolves) |
| `episode_resume` | Boot resumed a saved playhead | path |
| `commercial_start` | A commercial slot begins | path |
| `play` | Resume from pause | — |
| `pause` | User hit pause | — |
| `skip` | User clicked Skip in the UI | — |
| `button_press` | Bailout GPIO button pressed | — |
| `mode_change_playback` | Schedule woke up: episodes playing | — |
| `mode_change_off-hours` | Schedule expired: fan-art slideshow | — |

Receivers run in parallel goroutines with a **3-second timeout**, so a slow
or down endpoint never blocks playback. Failures log but don't retry — pick
a receiver that's idempotent or absorbs jitter.

### Example: Home Assistant

A simple HA automation that flashes the bar lights when the bailout fires:

```yaml
trigger:
  - platform: webhook
    webhook_id: jerry-bailout
condition: "{{ trigger.json.type == 'button_press' }}"
action:
  - service: light.turn_on
    target: { entity_id: light.bar_strip }
    data: { effect: "flash" }
```

Then add `https://homeassistant.local/api/webhook/jerry-bailout` to the
outbound list.

## Inbound: control endpoints

The same `/api/*` routes the dashboard uses are reachable from any HTTP
client on the LAN. They sit behind session auth — a long-lived cookie
works fine for headless integrations:

```sh
COOKIE=$(curl -sS -c - -d 'user=admin&password=YOUR_PW' \
    http://192.168.42.1/login | awk '/jerry_sid/ {print $7}')

# Skip the current episode
curl -sS -X POST -b "jerry_sid=$COOKIE" http://192.168.42.1/api/skip
```

Common control endpoints (all `POST`, all 204 on success):

- `/api/skip` — advance to next item (also fires the bumper-wipe + outbound webhook)
- `/api/play` — resume
- `/api/pause` — pause
- `/api/button/press` — simulate the GPIO bailout (dev/testing)
- `/api/queue/regenerate` — fresh shuffle with current weights/blacklist
- `/api/queue/rescan` — re-read USB drive (after hot-swap)

For the full surface — including episode-level weight/block toggles,
playlist CRUD, schedule management, network control — see
**API reference**.

## Notes / gotchas

- Outbound URLs are tried sequentially when there's more than one — total
  fan-out time scales with count. Keep the list short (<5).
- No HMAC signing yet. Treat receivers as trusted (private LAN).
- Inbound auth is session-cookie-based. If you need machine-to-machine
  access without going through `/login`, add a request to an outbound
  webhook that hits the appliance back — that loop tends to be cleaner
  than embedding credentials in your automation platform.
