# The Bailout button

A 30mm arcade button wired to the Pi's GPIO. Mounted under the bar or host
stand. Press it any time to instantly skip the current episode.

## When to use it

- A scene isn't landing in front of customers
- Same episode came up too soon (it shouldn't, but if it does)
- A regular asks for something different
- You just want to mix it up

## What it does internally

A press fires a falling-edge event on GPIO pin 17. The Go service catches
the event, kills mpv's current playback, and immediately advances to the
next file in the random rotation.

A debounce window (250ms) prevents accidental double-presses from skipping
two episodes in a row.

## If it stops working

1. Check the cable is fully seated on the Pi side.
2. From the admin UI, hit **Simulate GPIO** on the Now Playing page — if
   that advances the playlist, the software is fine and the button or
   wiring is at fault.
3. Replace the button (parts list in the **Hardware** topic).

## If you want a second button

The Pi has plenty of free GPIO pins. Common additions: an "Encore" button
that replays the last 30 seconds, or a "Volume up/down" pair. Open a
feature request and the firmware will be extended.
