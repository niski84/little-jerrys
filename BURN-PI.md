# Little Jerry's — Brian's setup guide

Download the image, flash a microSD card, then plug in a USB drive containing
Seinfeld. The Pi copies matching episodes onto its own SD card. After importing,
the USB drive can be removed.

## What you need

- Raspberry Pi **4**, its power supply, and an HDMI cable/TV.
- A **64 GB or larger microSD card** and card reader. The known collection is
  about 32 GiB before the operating system; a 32 GB card will not fit it.
  Larger or higher-bitrate collections need a larger card.
- A USB drive holding your episodes. **exFAT is recommended**; FAT32 and NTFS
  also work. Do not format a drive that already contains your only copy.
- An Ethernet cable to your router for the easiest access to Settings.
- A Windows, macOS, or Linux computer with Raspberry Pi Imager.

## 1. Download and flash

1. Open https://github.com/niski84/little-jerrys/releases and choose the
   **Pi 4 / USB import** release. A prerelease is usable for this handoff;
   its release notes explain the verification completed.
2. Under **Assets**, download `little-jerrys-<version>-pi4-arm64.img.xz` and
   this `BURN-PI.md` guide. The source-code ZIP/TAR files are not flash images.
   `SHA256SUMS` is available if you want to check the download.
3. Install Raspberry Pi Imager from https://www.raspberrypi.com/software/.
4. Select **Raspberry Pi 4**, then **Use custom** for the operating system.
   Choose the downloaded `.img.xz` file; Imager can decompress it while writing.
5. Select your microSD card. **Writing erases the selected card** — check the
   capacity and device name before starting.
6. Skip OS customization for this image, write, and let verification finish.
7. Eject the card, insert it into the Pi, connect HDMI and Ethernet, and power on.
   First boot expands the Linux partition to the full card. Allow a few minutes.
   The player is already installed; first boot does not download packages.

No code checkout, Linux build, terminal command, or SSH login is needed.

## 2. Put the episodes on a USB drive

Copy your video files onto the USB drive using Explorer/Finder/your file manager.
They can be at its root or inside show/season folders. Supported extensions:
`.mkv`, `.mp4`, `.m4v`, `.mov`, `.avi`, `.webm` (any letter case).

Common names the importer understands include:

```text
Seinfeld.S01E01.The.Seinfeld.Chronicles.mkv
Seinfeld - 1x01 - The Seinfeld Chronicles.mp4
Season 01/01 - The Seinfeld Chronicles.mkv
The Reverse Peephole.mkv
```

Punctuation and capitalization do not matter; uniquely identifiable small title
typos are tolerated. Explicit numbers use the bundled **aired-order catalog**.
If a title conflicts with its number, the importer reports the conflict for you
to fix. Arbitrary numbers or ambiguous titles are never used to guess an episode.

A combined two-part episode can use `S03E17E18` or `S03E17-18`. A sufficiently
long video with a shared two-part title, such as `The Boyfriend`, can cover both
catalog entries. Short or ambiguous files need explicit numbering. A combined
file plays once; selecting its second part starts that combined video from its
beginning. Put commercials in a `commercials` subfolder so they are ignored by
this episode importer.

When copying to USB finishes, eject the drive from your computer, then plug it
into the Pi. Use **one media USB drive at a time**, with a normal single data
partition. Linux/ext4 sources also work when their file and directory permissions
allow the `jerry` account to read them; exFAT avoids that permission issue.

## 3. Check the import

On a phone/computer on the same home network, open:

**http://little-jerrys.local:8089/settings**

If `.local` does not work, look for `little-jerrys` in your router's device list
and open `http://THE-PI-IP:8089/settings`.

Log in with **admin / admin** and choose a new password when prompted.

The first section in Settings shows:

- How many of the **180 episode entries across 9 seasons** are on the SD card.
- The current file and transfer progress, plus free SD space.
- Each file's copy/matching result and any failure details.
- An episode-by-episode checklist marking **On SD** or **Missing**.
- Whether the USB drive can be removed.

The USB drive is read-only to the appliance: originals are preserved. Copies are
written under a temporary name, checked with SHA-256, then published under a
standard episode filename. Completed episodes are skipped on later imports.
A restart preserves completed copies and retries the interrupted file. Keep the
USB connected and power on until Settings says no transfer is running. Copying
an entire library can take a while depending on the USB drive and SD card.

Episodes begin playing as they become available. When the import has finished,
remove the USB drive. The Pi plays from its SD card on future boots.

## Missing episodes, unmatched files, or failures

Open **USB import results and errors** in Settings. Fix the reported filename on
your computer, reconnect the USB, and select **Rescan / retry imports**. Common
fixes are adding `SxxExx`, separating ambiguous parts, completing a damaged source
copy, or providing more free SD space. Existing SD files are never overwritten.
Use the episode checklist to identify exactly what is still missing; a successful
transfer of every USB file does not imply the entire series is present.

For an advanced manual repair, the actual media directory on the Pi is:

```text
/opt/jerry/media
```

For example:

```text
/opt/jerry/media/s09e12_the_reverse_peephole.mkv
```

That is on the SD card's **Linux root partition**, not the small `bootfs` partition
Windows exposes. The easiest repair is renaming on the USB and retrying import.
Alternatively, shut the Pi down before removing its SD card; on a Linux computer,
mount the Linux root partition and copy into its `opt/jerry/media/` directory.
Keep the video readable by the Pi's `jerry` account, eject cleanly, reinsert into
the Pi, and select Rescan. SSH is disabled and there is no shared SSH password.

## If something is wrong

- **Settings is unreachable:** check Ethernet and the router's device list; keep
  `:8089` in the address. This image has no saved home Wi-Fi credentials.
- **No USB files appear:** reconnect one exFAT/FAT32/NTFS USB drive containing
  unpacked video files. Check that copying to USB has finished.
- **SD space errors:** use a larger card or free space; the importer reserves
  512 MiB so the OS can continue working.
- **Video does not play:** check the TV input and Pi 4 HDMI cable/port. Send Nick
  the visible Settings error or a photo of the screen. This release's hardware
  verification status is in its GitHub release notes.
