# Connecting to WiFi

The Pi works fully offline. Joining your restaurant's WiFi is optional but
unlocks: remote control from any device on the network, automatic content
updates, and outbound webhooks (e.g. open/close automation).

## First-time setup via the captive portal

When the Pi boots and doesn't see a known WiFi network, it starts its own:

1. On your phone, open WiFi settings and join `LittleJerrys_Config`.
2. Open a browser and visit `http://192.168.42.1`. The Pi's AP runs on
   `192.168.42.0/24` — an uncommon subnet picked specifically so it doesn't
   collide with the typical home / restaurant `192.168.{0,1,4}.x` ranges.
3. Sign in with `admin` / `admin`. Set a new password (required).
4. Open the **Network** tab and pick your restaurant's WiFi from the list.
5. Enter the password and hit **Join network**. The Pi reboots its WiFi
   stack and joins the chosen network.

After that, the Pi is reachable from any device on the same WiFi.

## Offline-only mode

You can keep the Pi running purely on its own AP forever — just bookmark
`http://192.168.4.1` and connect your phone to `LittleJerrys_Config` any
time you want to change settings. Nothing in the appliance requires
internet.

## When the Pi can't reach the internet

After ~30 seconds of failed connectivity, the Pi automatically falls back
to AP mode so you can reconfigure. This is logged. The TV continues
playing through the entire transition — no interruption.
