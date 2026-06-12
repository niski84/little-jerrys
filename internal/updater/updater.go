// Package updater handles over-the-air binary self-updates for the Little
// Jerry's appliance. On Pi hardware it runs as a background goroutine; in
// containers / dev it is a no-op (see Enabled()).
//
// Update flow:
//  1. Wait for boot delay (default 2 min) so playback is stable.
//  2. Poll GitHub Releases API for the latest tag.
//  3. If newer: download the jerry-arm64 asset into /opt/jerry/jerry.staged.
//  4. Record "update pending" flag in a sidecar file.
//  5. At a random minute inside the safe window (default 02:00–04:00 local)
//     call the apply script which atomically swaps the binary + restarts.
//  6. Re-check once per day inside the safe window so missed nights catch up.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	releaseAPI  = "https://api.github.com/repos/niski84/little-jerrys/releases/latest"
	assetName   = "jerry-arm64"
	stagedPath  = "/opt/jerry/jerry.staged"
	applyScript = "/opt/jerry/update-apply.sh"
	pendingFlag = "/opt/jerry/update.pending"
)

// NotifyFn is called when a new version has been staged and the apply is
// imminent. applyAt is when the binary swap will happen (after countdown).
// Callers use this to show OSD overlays and web banners.
type NotifyFn func(newVersion, releaseURL string, applyAt time.Time)

// Updater checks for new releases and schedules the swap.
type Updater struct {
	current     string // semver tag of the running binary, e.g. "v0.1.0"
	windowFrom  int    // hour (local) when the safe restart window opens
	windowTo    int    // hour (local) when it closes
	bootDelay   time.Duration
	countdown   time.Duration // warning period before the actual restart
	log         func(string)
	onAnnounce  NotifyFn // called when we're about to apply; may be nil
}

// New creates an Updater. current is the running version (e.g. "v0.1.0").
// windowFrom/To are local hours (24h). bootDelay is how long to wait after
// startup before the first check. countdown is how long to warn before
// the actual restart (e.g. 10*time.Minute).
func New(current string, windowFrom, windowTo int, bootDelay, countdown time.Duration, logFn func(string), notifyFn NotifyFn) *Updater {
	if logFn == nil {
		logFn = func(s string) { fmt.Println("[updater]", s) }
	}
	if countdown <= 0 {
		countdown = 10 * time.Minute
	}
	return &Updater{
		current:    current,
		windowFrom: windowFrom,
		windowTo:   windowTo,
		bootDelay:  bootDelay,
		countdown:  countdown,
		log:        logFn,
		onAnnounce: notifyFn,
	}
}

// Enabled reports whether the updater should run. It requires the apply
// script to exist (only present on a real Pi image build) and a non-dev
// version string. In containers and dev builds this returns false silently.
func Enabled() bool {
	_, err := os.Stat(applyScript)
	return err == nil
}

// Run starts the updater goroutine. It blocks until ctx is cancelled.
// Call as `go u.Run(ctx)`.
func (u *Updater) Run(ctx context.Context) {
	if !Enabled() {
		u.log("apply script not found — auto-update disabled (dev/container mode)")
		return
	}
	if !semver.IsValid(u.current) {
		u.log(fmt.Sprintf("version %q is not a valid semver tag — auto-update disabled", u.current))
		return
	}

	u.log(fmt.Sprintf("starting (version=%s window=%02d:00–%02d:00 bootDelay=%s)",
		u.current, u.windowFrom, u.windowTo, u.bootDelay))

	// Boot delay — wait for playback to stabilise before doing any network I/O.
	select {
	case <-ctx.Done():
		return
	case <-time.After(u.bootDelay):
	}

	// First check right after boot delay.
	u.checkAndStage(ctx)

	// Then re-check once every 12 hours so a previously staged update is
	// never more than half a day stale.
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.checkAndStage(ctx)
		}
	}
}

// checkAndStage looks for a newer release, downloads it if found, and
// schedules the apply step inside the safe window.
func (u *Updater) checkAndStage(ctx context.Context) {
	latest, assetURL, releaseURL, err := latestRelease(ctx)
	if err != nil {
		u.log(fmt.Sprintf("release check failed: %v", err))
		return
	}

	cmp := semver.Compare(latest, u.current)
	if cmp <= 0 {
		u.log(fmt.Sprintf("up to date (%s)", u.current))
		// If a previous download is staged but we're somehow already at that
		// version (e.g. manual re-burn), clean up.
		_ = os.Remove(stagedPath)
		_ = os.Remove(pendingFlag)
		return
	}

	u.log(fmt.Sprintf("new version available: %s → %s", u.current, latest))

	if err := u.download(ctx, assetURL, latest); err != nil {
		u.log(fmt.Sprintf("download failed: %v", err))
		return
	}

	u.log(fmt.Sprintf("%s staged — scheduling apply in safe window (%02d:00–%02d:00)",
		latest, u.windowFrom, u.windowTo))

	go u.waitAndApply(ctx, latest, releaseURL)
}

// download fetches the arm64 binary to stagedPath.
func (u *Updater) download(ctx context.Context, url, version string) error {
	u.log(fmt.Sprintf("downloading %s …", assetName))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "little-jerrys/"+u.current)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	// Write to a temp file in the same directory so rename is atomic.
	tmp := stagedPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create staged file: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write staged file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, stagedPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("stage rename: %w", err)
	}

	// Write a pending flag so the apply script can be triggered even if the
	// process was restarted between download and apply window.
	_ = os.WriteFile(pendingFlag, []byte(version+"\n"), 0644)

	u.log(fmt.Sprintf("staged %s (%s)", version, stagedPath))
	return nil
}

// waitAndApply sleeps until a random minute inside the safe window, announces
// the upcoming restart (fires onAnnounce + logs), waits the countdown, then
// invokes the apply script.
func (u *Updater) waitAndApply(ctx context.Context, version, releaseURL string) {
	delay := u.delayUntilWindow()
	u.log(fmt.Sprintf("apply scheduled in %s (version %s)", delay.Round(time.Minute), version))

	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}

	// Safe window reached — announce, then count down before applying.
	applyAt := time.Now().Add(u.countdown)
	u.log(fmt.Sprintf("safe window reached — announcing restart in %s (version %s)", u.countdown, version))

	if u.onAnnounce != nil {
		u.onAnnounce(version, releaseURL, applyAt)
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(u.countdown):
	}

	u.log(fmt.Sprintf("countdown elapsed — applying %s", version))
	// The jerry user has a sudoers entry granting NOPASSWD for this script.
	cmd := exec.CommandContext(ctx, "sudo", applyScript, version)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		u.log(fmt.Sprintf("apply script failed: %v — will retry next window", err))
	}
	// If the apply succeeded the process will be replaced by systemd before
	// this line runs. If it failed we log and the next checkAndStage cycle
	// will re-queue.
}

// delayUntilWindow returns a duration until a random minute within the next
// occurrence of [windowFrom, windowTo).
func (u *Updater) delayUntilWindow() time.Duration {
	now := time.Now()
	// Pick a random minute in the window.
	windowMinutes := (u.windowTo - u.windowFrom) * 60
	if windowMinutes <= 0 {
		windowMinutes = 60
	}
	offset := time.Duration(rand.Intn(windowMinutes)) * time.Minute

	target := time.Date(now.Year(), now.Month(), now.Day(),
		u.windowFrom, 0, 0, 0, now.Location()).Add(offset)

	if !target.After(now) {
		// Window already passed today — aim for tomorrow.
		target = target.Add(24 * time.Hour)
	}
	return time.Until(target)
}

// latestRelease queries the GitHub API and returns the latest semver tag,
// the binary asset download URL, and the human-facing release page URL.
func latestRelease(ctx context.Context) (tag, assetURL, releaseURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", "", fmt.Errorf("decode release JSON: %w", err)
	}

	tag = strings.TrimSpace(release.TagName)
	if !semver.IsValid(tag) {
		return "", "", "", fmt.Errorf("latest release tag %q is not valid semver", tag)
	}

	for _, a := range release.Assets {
		if a.Name == assetName {
			return tag, a.BrowserDownloadURL, release.HTMLURL, nil
		}
	}
	return "", "", "", fmt.Errorf("release %s has no asset named %q", tag, assetName)
}

// BinaryDir returns the directory containing the running binary, used to
// locate the apply script at runtime.
func BinaryDir() string {
	exe, err := os.Executable()
	if err != nil {
		return filepath.Dir(applyScript)
	}
	return filepath.Dir(exe)
}
