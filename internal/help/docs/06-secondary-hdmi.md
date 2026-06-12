# Secondary HDMI slideshow

The Pi 4/5 has two HDMI outputs. The first one runs the episodes (mpv);
the second one can run an independent slideshow of clip art — configurable
between a single image, a 2×2 grid, or a 4×4 grid that rotates every N
seconds.

## Configuring it

1. Open the admin UI → **Hours** tab.
2. Scroll to **Slideshow on HDMI-1**.
3. Tick **Enable HDMI-1 slideshow**, pick a layout, set seconds-per-cycle,
   save.
4. Hit **Preview in new tab** to verify it looks right before plugging in
   the second monitor.

Truly random — every image plays before any repeats, fresh shuffle each
pass. Pulled from `web/jerry/static/img/clipart/` baked into the binary;
swap files there + rebuild to change the catalog.

## Pi-side launch (chromium kiosk)

The slideshow is just an HTML page (`/slideshow`). Launch chromium kiosk on
HDMI-1 from a systemd unit on the Pi.

### X11 (Pi OS with X / older setups)

The second HDMI output is `:0.1`. Add to `/etc/systemd/system/jerry-slideshow.service`:

```
[Unit]
Description=Little Jerry's HDMI-1 slideshow
After=little-jerrys.service
Requires=little-jerrys.service

[Service]
User=pi
Environment=DISPLAY=:0.1
ExecStart=/usr/bin/chromium-browser --kiosk --noerrdialogs --disable-translate --no-first-run http://localhost:8089/slideshow
Restart=always

[Install]
WantedBy=graphical.target
```

### Wayland (default on Bookworm)

The second display is positioned at `1920,0` (assuming primary is 1080p).
Tell chromium where to land with `--window-position`:

```
ExecStart=/usr/bin/chromium-browser --kiosk --no-first-run \
    --window-position=1920,0 \
    --window-size=1920,1080 \
    http://localhost:8089/slideshow
```

If your TVs are arranged differently, run `wlr-randr` (Wayland) or
`xrandr -q` (X11) once to read out the actual coordinates and adjust.

## Disabling without unplugging

Toggle **Enable HDMI-1 slideshow** off in the admin UI. The page renders a
black screen with a small "slideshow disabled" indicator in the corner.
The chromium process stays alive — no service restart needed.
