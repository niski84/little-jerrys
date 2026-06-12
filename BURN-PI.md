# Little Jerry's — Brian's Setup Guide

Hey Brian. This gets your Pi playing Seinfeld on the TV with zero ongoing
maintenance. You do this once. After that, plug it in and it plays.

---

## What you need

- A Raspberry Pi 4 (2GB+ RAM) or Pi 5
- A microSD card, 32GB+ (Class 10 / A1 / A2)
- A microSD card reader for your laptop
- An HDMI cable
- A TV or monitor with HDMI in
- Your Seinfeld `.mp4` files
- A computer (Windows or Linux — both work, see below)

---

## Step 1 — Get the code

You need Git. If you don't have it:
- **Windows:** download from https://git-scm.com/download/win
- **Linux:** `sudo apt install git`

Then clone the repo:

```bash
git clone https://github.com/niski84/little-jerrys
cd little-jerrys
```

---

## Step 2 — Put your episode files in place

Create the media folder and copy your files in:

```bash
mkdir -p build/media/commercials
```

Then copy your `.mp4` files into `build/media/`. Name them like this:

```
build/media/
  s01e01_the_seinfeld_chronicles.mp4
  s01e02_the_stake_out.mp4
  s01e03_the_robbery.mp4
  s02e01_the_ex_girlfriend.mp4
  ...
  commercials/         ← optional, any filename
    ad_whatever.mp4
```

The `s01e01` part is what matters — season and episode number. Everything
after the underscore is just for your reference.

Supported formats: `.mp4` `.mkv` `.mov` `.avi` `.m4v` `.webm`

---

## Step 3 — Build the SD card image

### On Linux (or WSL2 on Windows — see note below)

Install the one dependency you probably don't have:

```bash
sudo apt install e2fsprogs parted curl xz-utils
```

Then build:

```bash
sudo ./scripts/build-pi-image.sh
```

This will:
1. Download Raspberry Pi OS Lite (~500 MB, first time only)
2. Cross-compile the Little Jerry's binary for Pi
3. Bake your episode files into the image
4. Produce `build/little-jerrys-YYYYMMDD.img`

Takes about 5–10 minutes depending on your internet and CPU.

### On Windows — use WSL2

WSL2 gives you a Linux environment inside Windows. The build script needs
loopback block device support — this works on WSL2 with Windows 11 / WSL
kernel 5.15+. If you're on Windows 10 or hit loopback errors, use a Linux
VM instead (VirtualBox with Ubuntu works fine).

To set up WSL2:

1. Open PowerShell as Administrator and run:
   ```
   wsl --install
   ```
2. Restart when prompted
3. Open "Ubuntu" from the Start menu — it'll finish setting up
4. Inside Ubuntu, install deps and navigate to the repo:
   ```bash
   sudo apt install e2fsprogs parted curl xz-utils
   cd /mnt/c/Users/YourName/path/to/little-jerrys
   ```
5. Then follow the Linux build instructions above

---

## Step 4 — Burn the image to the SD card

### Option A: Raspberry Pi Imager (easiest, works on Windows and Linux)

1. Download from https://www.raspberrypi.com/software/
2. Open it and choose:
   - **Raspberry Pi Device** → Raspberry Pi 4
   - **Operating System** → scroll down → "Use custom" → pick your `.img` file from `build/`
   - **Storage** → your SD card
3. Click **Next**
4. When it asks about OS customization → click **No** (everything is already configured)
5. Click **Yes** to confirm. Takes 5–10 minutes.

### Option B: Command line on Linux

```bash
# Find your SD card — look for something like /dev/sdb or /dev/mmcblk0
lsblk

# Write the image (replace /dev/sdX with your actual SD card)
sudo dd if=build/little-jerrys-*.img of=/dev/sdX bs=4M conv=fsync status=progress
sudo sync
```

**Warning:** double-check the device path. Wrong device = wiped disk.

---

## Step 5 — First boot

1. Eject the SD card from your laptop
2. Insert it into the Pi
3. Plug HDMI into your TV
4. Plug in the Pi's power cable

**What happens:**
- First boot takes about 2–3 minutes (installs mpv, sets up the player)
- After that, it goes straight to playing episodes — full screen, no login, no setup
- Every boot after the first takes about 30 seconds

**First boot requires internet** (ethernet cable or WiFi configured in advance)
so it can install mpv. If you don't have ethernet handy, the Pi will broadcast
a WiFi network called `Channel14_Setup` — connect to it from your phone,
join it to your home WiFi, then reboot.

---

## Web control panel

Once it's on your network, open a browser on any device and go to:

```
http://little-jerrys.local
```

Or find the Pi's IP from your router and use that directly.

Default login: **admin / admin** — you'll be asked to change it on first login.
If you ever lock yourself out, the rescue password is `jerry-rescue`.

---

## Recovery

If the screen is black and nothing's playing:

1. SSH in: `ssh pi@little-jerrys.local` (password: `jerry`)
2. Check logs: `sudo journalctl -u jerry -n 50`
3. Check firstboot: `sudo cat /var/log/jerry-firstboot.log`

Send me the output and I'll tell you what went wrong.

---

## Checklist before you call it done

- [ ] Video is playing full screen on the TV
- [ ] Skipped a track (web panel → dashboard → Skip, or wire up the arcade button)
- [ ] Changed the admin password
- [ ] Confirmed it auto-resumed after a reboot
