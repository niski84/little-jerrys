// Package jerry is the main app domain — the Smash Deck-style wire-up that
// glues the player, playlist, GPIO, auth, and HTTP layers together.
package jerry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niski84/little-jerrys/internal/auth"
	"github.com/niski84/little-jerrys/internal/clipart"
	"github.com/niski84/little-jerrys/internal/eventlog"
	"github.com/niski84/little-jerrys/internal/gpio"
	"github.com/niski84/little-jerrys/internal/jerry/views"
	"github.com/niski84/little-jerrys/internal/mediaroot"
	"github.com/niski84/little-jerrys/internal/network"
	"github.com/niski84/little-jerrys/internal/player"
	"github.com/niski84/little-jerrys/internal/playlist"
	"github.com/niski84/little-jerrys/internal/schedule"
	"github.com/niski84/little-jerrys/internal/splash"
	"github.com/niski84/little-jerrys/internal/tmdb"
	"github.com/niski84/little-jerrys/internal/usbconfig"
	"github.com/niski84/little-jerrys/internal/webhook"
	"github.com/niski84/little-jerrys/web"
)

// Mode is what the appliance is currently doing. Off-hours mode swaps the
// random episode rotation for a fan-art slideshow.
type Mode int

const (
	ModePlayback Mode = iota
	ModeOffHours
)

func (m Mode) String() string {
	if m == ModeOffHours {
		return "off-hours"
	}
	return "playback"
}

// App is the runtime container for the Little Jerry's Automator.
type App struct {
	Cfg         Config
	Store       *Store
	Auth        *auth.Manager
	Player      player.Player
	Playlist    *playlist.Engine
	Button      gpio.Listener
	Webhook     *webhook.Sender
	Network     *network.Manager
	TMDB        *tmdb.Cache
	Events      *eventlog.Log
	waitingPath string
	cancel      context.CancelFunc
	startOnce   sync.Once
	workerWG    sync.WaitGroup
	closing     atomic.Bool

	// currentTrack is what's actually playing right now (vs. Playlist's
	// internal queue head). Updated by PlayNext / ResumeOrPlayNext, read by
	// the position-saver goroutine.
	currentMu sync.RWMutex
	current   string

	// Mode reflects whether we're in normal playback or off-hours slideshow.
	modeMu sync.RWMutex
	// ModePlayback is the zero value. NewApp initializes this to an invalid
	// sentinel so boot cannot be mistaken for an already-applied mode.
	mode Mode

	// Playback tracker state — flipped to true the first time the current
	// track crosses the completion threshold (75% of duration). Reset on
	// PlayNext so the next track starts uncredited. Avoids double-counting
	// when the tracker ticks repeatedly while a track is past 75%.
	creditMu      sync.Mutex
	creditedTrack string
	quietMu       sync.Mutex
	quietActive   bool
	advanceMu     sync.Mutex // serialize web skip, GPIO bailout, and EOF auto-advance
	// Pending OTA update announcement. Set by the updater goroutine; read
	// by the /api/update/status endpoint and the dashboard banner.
	updateMu     sync.RWMutex
	updateNotice *UpdateNotice
}

// UpdateNotice holds the details of a staged OTA update that is pending
// a maintenance restart.
type UpdateNotice struct {
	Version    string    `json:"version"`
	ReleaseURL string    `json:"release_url"`
	ApplyAt    time.Time `json:"apply_at"`
}

// SetUpdateNotice stores the pending update announcement.
func (a *App) SetUpdateNotice(n *UpdateNotice) {
	a.updateMu.Lock()
	a.updateNotice = n
	a.updateMu.Unlock()
}

// UpdateNotice returns the pending update, or nil if none.
func (a *App) GetUpdateNotice() *UpdateNotice {
	a.updateMu.RLock()
	defer a.updateMu.RUnlock()
	return a.updateNotice
}

// CurrentMode returns the active mode (playback vs. off-hours).
func (a *App) CurrentMode() Mode {
	a.modeMu.RLock()
	defer a.modeMu.RUnlock()
	return a.mode
}

// NewApp initializes all subsystems and starts the playback loop.
func NewApp(ctx context.Context, cfg Config) (*App, error) {
	ctx, cancel := context.WithCancel(ctx)
	ready := false
	defer func() {
		if !ready {
			cancel()
		}
	}()
	// Resolve once at boot so every subsystem (branding, uploads, playlist,
	// and playback) agrees on the same drive. The watcher below re-scans this
	// path after hot-plug; JERRY_MEDIA_ROOT remains the manual override.
	cfg.MediaRoot = mediaroot.Resolve(cfg.MediaRoot)
	// USB-side operator config (jerry.conf) is the lowest-friction way to
	// rebrand a unit without touching the admin UI — read it once at boot
	// and apply the overrides where they're consumed (nav logo, AP SSID).
	uc := usbconfig.Load(cfg.MediaRoot)
	if uc.TenantName != "" {
		views.BrandTenantName = uc.TenantName
	}
	if uc.LogoFile != "" {
		// Served by the /branding/* HTTP handler below, mounted on MediaRoot.
		views.BrandLogoURL = "/branding/" + uc.LogoFile
	}
	if uc.APssid != "" {
		network.SetAPssid(uc.APssid)
	}

	events, eventErr := eventlog.NewPersistent(filepath.Join(filepath.Dir(cfg.StatePath), "events.jsonl"), 1000)
	if eventErr != nil {
		events = eventlog.New(200)
		events.Warn("eventlog", "Persistent history unavailable; exporter reports a coverage gap")
	}
	initialized := false
	defer func() {
		if !initialized {
			_ = events.Close()
		}
	}()
	events.Info("boot", "Little Jerry's starting up")

	store, err := NewStore(cfg.StatePath)
	if err != nil {
		return nil, fmt.Errorf("state store: %w", err)
	}

	// Ensure a signing key exists — generated once, persisted in state so
	// sessions survive restarts. Without this, every restart logs everyone out.
	sessionKey := store.Get().SessionKey
	if sessionKey == "" {
		sessionKey = auth.GenerateKey()
		if err := store.Update(func(s *State) { s.SessionKey = sessionKey }); err != nil {
			return nil, fmt.Errorf("persist session key: %w", err)
		}
	}
	authMgr, err := auth.New(passwordAdapter{s: store}, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("auth manager: %w", err)
	}
	if cfg.RescuePassword != "" {
		authMgr.SetRescuePassword(cfg.RescuePassword)
		fmt.Printf("[jerry] rescue password active (env JERRY_RESCUE_PASSWORD)\n")
	}

	pl := playlist.New(cfg.MediaRoot)
	if err := pl.Scan(); err != nil {
		fmt.Printf("[jerry] media scan warning: %v\n", err)
	}

	st := store.Get()
	pl.SetBlacklist(st.Blacklist)
	for key, weight := range st.Weights {
		pl.SetEpisodeWeight(key, weight)
	}
	commercialBlacklist, commercialWeights := effectiveCommercialSettings(st)
	pl.SetCommercialBlacklist(commercialBlacklist)
	pl.SetCommercialWeights(commercialWeights)
	pl.SetBreakSchedule(st.BreakAfterMinutes, st.CommercialsPerBreak)
	pl.EnableCommercials(st.CommercialsEnabled)

	wh := webhook.NewSender()
	wh.SetURLs(st.WebhookOutbound)

	// Prepare a branded interstitial outside the video files so mpv never
	// exposes tty1 between episodes.
	waitingPath := filepath.Join(filepath.Dir(cfg.StatePath), "waiting.png")
	logoPath := ""
	if uc.LogoFile != "" {
		logoPath = filepath.Join(cfg.MediaRoot, uc.LogoFile)
	}
	if err := splash.RenderWaiting(views.BrandTenantName, logoPath, waitingPath); err != nil {
		fmt.Printf("[jerry] interstitial unavailable: %v\n", err)
		waitingPath = ""
	}

	var p player.Player
	if cfg.UsePlayerStub {
		p = player.NewStub()
	} else {
		// Close saves the last position before terminating mpv.
		p, err = player.New(context.WithoutCancel(ctx), player.Config{
			SocketPath: cfg.MpvSocket,
			Fullscreen: true,
			HWDecode:   true,
			AudioOut:   cfg.MpvAO,
			VideoOut:   cfg.MpvVO,
		})
		if err != nil {
			fmt.Printf("[jerry] mpv unavailable, falling back to stub: %v\n", err)
			p = player.NewStub()
		}
	}

	defer func() {
		if !initialized {
			_ = p.Close()
		}
	}()

	btn := gpio.New(gpio.Config{
		Pin:       cfg.GPIOPin,
		PullUp:    true,
		Simulated: cfg.UseGPIOSimulated,
	})

	netMgr := network.New()
	// The captive-portal Watcher actively manages the host's WiFi (AP-mode
	// switching, connectivity polling) and is only safe on a dedicated Pi
	// appliance. It is OFF unless jerry.conf sets CAPTIVE_PORTAL=true, so a
	// fresh install on any normal machine never touches networking.
	if uc.CaptivePortal && netMgr.Available() {
		// 30s grace: let NetworkManager try the saved client SSID and allow a
		// home/update server to become reachable. After that, if we still
		// don't have client connectivity, broadcast the AP so the staff
		// can configure us from a phone.
		fmt.Printf("[network] captive portal ENABLED — managing WiFi via nmcli\n")
		// Started below, after app exists, so shutdown can join this worker.
	} else {
		fmt.Printf("[network] captive portal disabled (set CAPTIVE_PORTAL=true in jerry.conf to enable)\n")
	}

	// TMDB cache is read-only — populated at build time by --prefetch-tmdb.
	// Empty cache (no prefetch yet) is fine; the browse UI degrades.
	tmdbCache := tmdb.NewCache(tmdb.EmbeddedFS)

	app := &App{
		Cfg:         cfg,
		Store:       store,
		Auth:        authMgr,
		Player:      p,
		Playlist:    pl,
		Button:      btn,
		Webhook:     wh,
		Network:     netMgr,
		TMDB:        tmdbCache,
		Events:      events,
		mode:        Mode(-1),
		waitingPath: waitingPath,
		cancel:      cancel,
	}
	if uc.CaptivePortal && netMgr.Available() {
		app.workerWG.Add(1)
		go func() { defer app.workerWG.Done(); netMgr.Watcher(ctx, 30*time.Second) }()
	}
	_ = app.Player.Volume(volumePercent(st.VolumeLevel))

	if err := btn.Start(ctx, func() {
		app.Events.Info("gpio", "BAILOUT pressed — skipping current track")
		wh.Fire(webhook.Event{Type: "button_press"})
		_ = app.PlayNext()
	}); err != nil {
		app.Events.Warnf("gpio", "listener init: %v", err)
	}

	// Auto-advance: when mpv finishes a file naturally (--keep-open=no
	// makes it idle to a black screen), kick the next item in the queue.
	// Without this, the TV goes dark at the end of every episode and
	// nothing brings it back until somebody clicks Skip in the UI.
	p.OnEndFile(app.advanceAfterEnd)

	// Restore the saved queue so the dashboard's "Up next" populates
	// immediately on boot. If the saved queue is empty (fresh install or
	// last shutdown happened mid-rotation tail), and we have media files,
	// auto-regenerate so the user always sees what's coming up without
	// having to click anything.
	if len(st.QueueItems) > 0 {
		items := make([]playlist.Item, 0, len(st.QueueItems))
		for _, q := range st.QueueItems {
			kind := playlist.KindEpisode
			if q.Kind == "commercial" {
				kind = playlist.KindCommercial
			}
			items = append(items, playlist.Item{Kind: kind, Path: q.Path, EstSecs: q.EstSecs})
		}
		pl.LoadQueue(items)
		// Validate head file exists — USB may have changed since last boot.
		// If stale, rescan and regenerate so the appliance plays immediately.
		head := pl.Peek(1)
		if len(head) > 0 {
			if _, statErr := os.Stat(head[0].Path); statErr != nil {
				fmt.Printf("[jerry] saved queue is stale (head missing), rescanning...\n")
				_ = pl.Scan()
				if len(pl.Catalog()) > 0 {
					pl.Regenerate()
					fmt.Printf("[jerry] regenerated fresh shuffle (%d items)\n", pl.Stats().QueueRemaining)
				}
			} else {
				fmt.Printf("[jerry] restored %d-item queue from state\n", len(items))
			}
		}
	} else if len(pl.Catalog()) > 0 {
		pl.Regenerate()
		fmt.Printf("[jerry] no saved queue — generated fresh shuffle (%d items)\n", pl.Stats().QueueRemaining)
	}

	// Background position saver — every 10s persists the current track and
	// playhead so a power-off at night picks back up the next morning.
	app.workerWG.Add(1)
	go func() { defer app.workerWG.Done(); app.savePositionLoop(ctx, 10*time.Second) }()

	// Playback tracker — every 5s records minutes played vs paused and
	// credits a "completion" the first time the playhead crosses 75%.
	// Built so the weekly summary and per-episode "watched N times" both
	// have ground-truth signal instead of just "started N times".
	app.workerWG.Add(1)
	go func() { defer app.workerWG.Done(); app.playbackTracker(ctx, 5*time.Second) }()

	// Schedule watcher — every minute, decide if we should be in playback
	// or off-hours mode and switch if needed.
	app.workerWG.Add(1)
	go func() { defer app.workerWG.Done(); app.modeWatcher(ctx, time.Minute) }()

	// Media watcher — rescans every 30s so a USB drive plugged in after boot
	// is picked up automatically and playback starts without any manual step.
	app.workerWG.Add(1)
	go func() { defer app.workerWG.Done(); app.mediaWatcher(ctx, 30*time.Second) }()

	initialized = true
	ready = true
	return app, nil
}

// ShowBootScreen puts the appliance's branded card on HDMI before playback
// initialization. Hold it long enough for someone walking past the display
// to read the branded message and setup details before playback begins.
func (a *App) ShowBootScreen() {
	if a.waitingPath == "" {
		return
	}
	if err := a.Player.Play(a.waitingPath); err != nil {
		fmt.Printf("[jerry] boot screen unavailable: %v\n", err)
		return
	}
	time.Sleep(10 * time.Second)
}

// mediaWatcher rescans the media root on each tick. When new files appear
// (USB plugged in after boot) and the queue is empty, it auto-generates a
// fresh shuffle and starts playback — no manual "Regenerate" required.
func (a *App) mediaWatcher(ctx context.Context, interval time.Duration) {
	lastCount := len(a.Playlist.Catalog())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.Playlist.Scan(); err != nil {
				continue
			}
			newCount := len(a.Playlist.Catalog())
			if newCount == lastCount {
				continue
			}
			lastCount = newCount
			// Files appeared. If the queue is empty AND nothing is currently
			// playing, start fresh. Don't interrupt active playback.
			if newCount > 0 && len(a.Playlist.Peek(1)) == 0 {
				a.Playlist.Regenerate()
				fmt.Printf("[jerry] media changed (%d files) — auto-generated playlist\n", newCount)
				// Only auto-play if the player is genuinely idle (no current track).
				if a.Current() == "" {
					if err := a.PlayNext(); err != nil {
						a.Events.Warnf("playback", "media watcher auto-play: %v", err)
					}
				}
			}
		}
	}
}

// modeWatcher ticks at interval, evaluates the configured schedules, and
// switches mode if reality doesn't match. The initial mode is started by
// StartCurrentMode after the boot card; doing it here would race the main
// startup path and issue two mpv loadfile commands.
func (a *App) modeWatcher(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.applyQuietMode(time.Now())
			a.applyDesiredMode(false)
		}
	}
}

// StartCurrentMode starts the mode selected at boot after the branded card
// has finished. Keeping this explicit prevents modeWatcher from starting
// playback concurrently with main's startup handoff.
func (a *App) StartCurrentMode() {
	a.startOnce.Do(func() {
		a.applyQuietMode(time.Now())
		a.applyDesiredMode(true)
	})
}

// applyQuietMode mutes only HDMI audio; playback and episode progression
// continue normally. Start/end may cross midnight (for example 22:00–07:00).
func (a *App) applyQuietMode(now time.Time) {
	st := a.Store.Get()
	quiet := false
	start, startErr := time.Parse("15:04", st.QuietStart)
	end, endErr := time.Parse("15:04", st.QuietEnd)
	if st.QuietModeEnabled && startErr == nil && endErr == nil {
		cur := now.Hour()*60 + now.Minute()
		s := start.Hour()*60 + start.Minute()
		e := end.Hour()*60 + end.Minute()
		if s < e {
			quiet = cur >= s && cur < e
		} else if s > e {
			quiet = cur >= s || cur < e
		}
	}
	a.quietMu.Lock()
	defer a.quietMu.Unlock()
	if quiet != a.quietActive {
		if err := a.Player.Mute(quiet); err != nil {
			fmt.Printf("[jerry] quiet mode mute=%t: %v\n", quiet, err)
			return
		}
		a.quietActive = quiet
	}
}

// applyDesiredMode switches the player between playback and slideshow when
// the schedule says it's time. force=true bypasses the no-op check (used at
// boot and after schedule edits in the admin UI).
func (a *App) applyDesiredMode(force bool) {
	st := a.Store.Get()
	now := time.Now()

	// No schedules configured = always-on (the appliance just works).
	target := ModePlayback
	if len(st.Schedules) > 0 {
		active, _ := schedule.Active(st.Schedules, now)
		if !active {
			target = ModeOffHours
		}
	}

	a.modeMu.Lock()
	prev := a.mode
	a.mode = target
	a.modeMu.Unlock()

	if !force && prev == target {
		return
	}
	a.Events.Infof("schedule", "mode → %s", target)
	a.Webhook.Fire(webhook.Event{Type: "mode_change_" + target.String()})

	switch target {
	case ModePlayback:
		// Resume mid-episode if we have a saved position; otherwise next.
		_ = a.PlayCurrent()
	case ModeOffHours:
		if !st.FanArtEnabled {
			return
		}
		fsRoot, err := web.StaticFS()
		if err != nil {
			fmt.Printf("[jerry] off-hours: static FS unavailable: %v\n", err)
			return
		}
		urls, _ := clipart.Scan(fsRoot)
		if len(urls) == 0 {
			fmt.Printf("[jerry] off-hours: no embedded clipart images\n")
			return
		}
		// mpv accepts http:// URLs — serve embedded images from the local web server.
		base := "http://localhost:" + a.Cfg.Port
		images := make([]string, len(urls))
		for i, u := range urls {
			images[i] = base + u
		}
		a.setCurrent("")
		if err := a.Player.PlaySlideshow(images, st.FanArtSecsPerImage); err != nil {
			fmt.Printf("[jerry] slideshow start: %v\n", err)
		}
	}
}

// PlayCurrent starts the rotation. If a saved last-track + position exists
// and the file is still on the USB drive, mpv resumes there. Otherwise picks
// a fresh random episode. Call this once at startup.
//
// Respects the schedule: if we're currently outside operating hours, this
// is a no-op — the modeWatcher will start the slideshow on its first tick.
func (a *App) PlayCurrent() error {
	if a.CurrentMode() == ModeOffHours {
		return nil
	}
	st := a.Store.Get()
	if st.LastTrack != "" {
		if _, err := os.Stat(st.LastTrack); err == nil {
			fmt.Printf("[jerry] resuming %s @ %.1fs\n", st.LastTrack, st.LastPositionSecs)
			a.setCurrent(st.LastTrack)
			pos := time.Duration(st.LastPositionSecs * float64(time.Second))
			if err := a.Player.PlayAt(st.LastTrack, pos); err != nil {
				return fmt.Errorf("resume: %w", err)
			}
			a.showEpisodeOverlay(st.LastTrack)
			a.Webhook.Fire(webhook.Event{Type: "episode_resume", Path: st.LastTrack})
			return nil
		}
		fmt.Printf("[jerry] saved track no longer on USB, starting fresh: %s\n", st.LastTrack)
	}
	return a.PlayNext()
}

// playbackTracker is a 5-second-tick goroutine that maintains two pieces
// of "real play" state: cumulative minutes watched / paused, and a
// per-episode completion credit awarded when the playhead crosses 75% of
// the track's duration. The 75% threshold is the standard "completed
// view" definition cribbed from streaming services — long enough that
// scrub-skips don't count, short enough that watching credits/end-tag
// scenes still does.
func (a *App) playbackTracker(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.playbackTick(interval)
		}
	}
}

// mondayOf returns the Monday on or before t (week start). Used to detect
// week-boundary crossings for the weekly stats rollup.
func mondayOf(t time.Time) time.Time {
	wd := int(t.Weekday())
	// Sunday is 0 in Go; we want Monday-as-start, so map Sun→6, Mon→0, etc.
	delta := (wd + 6) % 7
	return time.Date(t.Year(), t.Month(), t.Day()-delta, 0, 0, 0, 0, t.Location())
}

// ensureWeekBoundary checks whether the current Monday differs from the
// stored WeekStart. If so, freezes the prior week's rollup into
// LastWeekRollup, then snapshots the current totals into the WeekStart
// fields. Idempotent — calling more than once per week is a no-op.
//
// Called every playbackTracker tick so the boundary always lands within
// 5 seconds of midnight Monday morning regardless of when the appliance
// was running.
func (a *App) ensureWeekBoundary() {
	today := mondayOf(time.Now()).Format("2006-01-02")
	st := a.Store.Get()
	if st.WeekStart == today {
		return
	}
	_ = a.Store.Update(func(s *State) {
		// Roll the previous week (if there was one) into LastWeekRollup.
		if s.WeekStart != "" {
			s.LastWeekRollup = computeWeekRollup(*s)
		}
		// Snapshot today's totals as the new week's baseline.
		s.WeekStart = today
		s.WeekStartPlayCounts = cloneIntMap(s.PlayCounts)
		s.WeekStartCompletedCounts = cloneIntMap(s.CompletedCounts)
		s.WeekStartMinutesPlayed = s.MinutesPlayed
		s.WeekStartMinutesPaused = s.MinutesPaused
	})
}

// ComputeCurrentWeekRollup is the live (un-rolled) version, used by the
// /summary view to render "this week" without waiting for a Monday.
func (a *App) ComputeCurrentWeekRollup() *WeekRollup {
	return computeWeekRollup(a.Store.Get())
}

func computeWeekRollup(s State) *WeekRollup {
	r := &WeekRollup{
		Start:         s.WeekStart,
		MinutesPlayed: s.MinutesPlayed - s.WeekStartMinutesPlayed,
		MinutesPaused: s.MinutesPaused - s.WeekStartMinutesPaused,
		PerEpisode:    map[string]int{},
	}
	if r.MinutesPlayed < 0 {
		r.MinutesPlayed = 0
	}
	if r.MinutesPaused < 0 {
		r.MinutesPaused = 0
	}
	for k, v := range s.CompletedCounts {
		delta := v - s.WeekStartCompletedCounts[k]
		if delta > 0 {
			r.PerEpisode[k] = delta
			r.EpisodesCompleted += delta
			r.UniqueEpisodes++
		}
	}
	for k, v := range s.PlayCounts {
		delta := v - s.WeekStartPlayCounts[k]
		if delta > 0 {
			r.EpisodesStarted += delta
		}
	}
	return r
}

func cloneIntMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (a *App) playbackTick(interval time.Duration) {
	// Roll the week over if we've crossed a Monday boundary. Cheap when
	// there's nothing to do (just compares two date strings).
	a.ensureWeekBoundary()

	a.currentMu.RLock()
	track := a.current
	a.currentMu.RUnlock()
	if track == "" {
		return
	}
	pos, perr := a.Player.Position()
	dur, derr := a.Player.Duration()
	paused := a.Player.Status().Paused
	deltaMin := interval.Minutes()

	// Decide whether THIS tick is the one that credits completion.
	var creditCompletion bool
	if perr == nil && derr == nil && dur.Seconds() > 0 {
		if pos.Seconds()/dur.Seconds() >= 0.75 {
			a.creditMu.Lock()
			if a.creditedTrack != track {
				a.creditedTrack = track
				creditCompletion = true
			}
			a.creditMu.Unlock()
		}
	}

	if err := a.Store.Update(func(s *State) {
		if paused {
			s.MinutesPaused += deltaMin
		} else {
			s.MinutesPlayed += deltaMin
		}
		if creditCompletion {
			if s.CompletedCounts == nil {
				s.CompletedCounts = map[string]int{}
			}
			s.CompletedCounts[track]++
		}
	}); err != nil {
		fmt.Printf("[jerry] playback tracker save: %v\n", err)
	}
	if creditCompletion {
		a.Events.Infof("playback", "completed (≥75%%): %s", filepath.Base(track))
	}
}

// savePositionLoop persists current playhead at a fixed interval. Stops when
// ctx is canceled. Errors are logged, not surfaced — never block playback.
//
// We deliberately don't do a final save on ctx.Done. In production the
// failure mode is the Pi losing AC power (not a graceful SIGTERM), so the
// 10s ticker is the durability guarantee. In tests, skipping the final save
// keeps state.json.tmp from racing with t.TempDir cleanup.
func (a *App) savePositionLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.savePosition()
		}
	}
}

func (a *App) savePosition() {
	a.currentMu.RLock()
	track := a.current
	a.currentMu.RUnlock()
	if track == "" {
		return
	}
	pos, err := a.Player.Position()
	if err != nil || pos <= 0 {
		return
	}
	if err := a.Store.Update(func(s *State) {
		s.LastTrack = track
		s.LastPositionSecs = pos.Seconds()
	}); err != nil {
		fmt.Printf("[jerry] save position: %v\n", err)
	}
}

func (a *App) setCurrent(path string) {
	a.currentMu.Lock()
	a.current = path
	a.currentMu.Unlock()
}

func (a *App) Current() string {
	a.currentMu.RLock()
	defer a.currentMu.RUnlock()
	return a.current
}

// PlayNext advances to the next item in the random rotation. Resets the
// stored position — once we've moved on, we don't want to resume the
// previous episode on next boot. Cumulative play count increments for
// episode items only (commercials don't count).
func (a *App) PlayNext() error {
	a.advanceMu.Lock()
	defer a.advanceMu.Unlock()
	if a.closing.Load() {
		return fmt.Errorf("app is shutting down")
	}
	return a.playNextLocked()
}

// Reject an EOF from a track replaced by a manual skip or a mode change.
// Interstitial and next load share the same lock as manual/GPIO advances.
func (a *App) advanceAfterEnd(endedPath string) {
	a.advanceMu.Lock()
	defer a.advanceMu.Unlock()
	if a.closing.Load() {
		return
	}
	if a.Current() != endedPath || a.CurrentMode() != ModePlayback {
		return
	}
	if a.waitingPath != "" {
		if err := a.Player.Play(a.waitingPath); err != nil {
			a.Events.Warnf("playback", "interstitial: %v", err)
		} else {
			time.Sleep(900 * time.Millisecond)
		}
	}
	if a.closing.Load() {
		return
	}
	if err := a.playNextLocked(); err != nil {
		a.Events.Warnf("playback", "auto-advance failed: %v", err)
	}
}

func (a *App) playNextLocked() error {
	next, err := a.Playlist.Next()
	if err != nil {
		return fmt.Errorf("playlist next: %w", err)
	}
	// Skip files that no longer exist (USB swapped, files deleted).
	// Try up to 20 times before giving up so a mostly-valid queue
	// recovers gracefully without getting stuck.
	for i := 0; i < 20; i++ {
		if _, statErr := os.Stat(next.Path); statErr == nil {
			break
		}
		a.Events.Warnf("playback", "file missing, skipping: %s", filepath.Base(next.Path))
		next, err = a.Playlist.Next()
		if err != nil {
			return fmt.Errorf("playlist next (skip missing): %w", err)
		}
	}
	a.setCurrent(next.Path)
	// Each new track gets a fresh shot at being credited as "completed."
	a.creditMu.Lock()
	a.creditedTrack = ""
	a.creditMu.Unlock()
	if err := a.Player.Play(next.Path); err != nil {
		return fmt.Errorf("play: %w", err)
	}
	a.showEpisodeOverlay(next.Path)
	_ = a.Store.Update(func(s *State) {
		s.LastTrack = next.Path
		s.LastPositionSecs = 0
		if next.Kind == playlist.KindEpisode {
			if s.PlayCounts == nil {
				s.PlayCounts = map[string]int{}
			}
			s.PlayCounts[next.Path]++
		}
	})
	a.SnapshotQueue()
	evt := webhook.Event{Type: "episode_start", Path: next.Path}
	if next.Kind == playlist.KindCommercial {
		evt.Type = "commercial_start"
	} else if s, e, ok := tmdb.SuggestFileMatch(next.Path); ok {
		evt.Season = s
		evt.Episode = e
		if ep, err := a.TMDB.Episode(s, e); err == nil {
			evt.Title = ep.Name
			evt.Synopsis = ep.Overview
		}
	}
	a.Webhook.Fire(evt)
	return nil
}

func volumeLevel(level int) int {
	if level < 1 || level > 10 {
		return 5
	}
	return level
}

func volumePercent(level int) int {
	// HDMI/monitor volume is perceived logarithmically. Keep the ten user
	// points in the useful audible range instead of wasting most of them below
	// the monitor's practical threshold.
	curve := [...]int{50, 60, 68, 75, 81, 86, 90, 94, 97, 100}
	return curve[volumeLevel(level)-1]
}

func (a *App) showEpisodeOverlay(path string) {
	s, e, ok := tmdb.SuggestFileMatch(filepath.Base(path))
	if !ok {
		return
	}
	ep, err := a.TMDB.Episode(s, e)
	if err != nil {
		return
	}
	if err := a.Player.ShowEpisodeOverlay(ep.Name, ep.Overview, s, e); err != nil {
		fmt.Printf("[jerry] episode overlay unavailable: %v\n", err)
	}
}

// SnapshotQueue captures the engine's current upcoming items into State so
// a reboot doesn't lose the lineup. Cheap — runs after every mutation.
func (a *App) SnapshotQueue() {
	items := a.Playlist.Peek(0) // 0 = full queue
	snap := make([]QueueSnapshotItem, 0, len(items))
	for _, it := range items {
		kind := "episode"
		if it.Kind == playlist.KindCommercial {
			kind = "commercial"
		}
		snap = append(snap, QueueSnapshotItem{Kind: kind, Path: it.Path, EstSecs: it.EstSecs})
	}
	_ = a.Store.Update(func(s *State) { s.QueueItems = snap })
}

// ApplyState pushes State changes through to the live playlist + webhook
// subsystems. Call after Store.Update() so runtime reflects the new config.
func (a *App) ApplyState() {
	st := a.Store.Get()
	a.Playlist.SetBlacklist(st.Blacklist)
	for key, weight := range st.Weights {
		a.Playlist.SetEpisodeWeight(key, weight)
	}
	commercialBlacklist, commercialWeights := effectiveCommercialSettings(st)
	a.Playlist.SetCommercialBlacklist(commercialBlacklist)
	a.Playlist.SetCommercialWeights(commercialWeights)
	a.Playlist.SetBreakSchedule(st.BreakAfterMinutes, st.CommercialsPerBreak)
	a.Playlist.EnableCommercials(st.CommercialsEnabled)
	_ = a.Player.Volume(volumePercent(st.VolumeLevel))
	a.Webhook.SetURLs(st.WebhookOutbound)
	a.applyQuietMode(time.Now())
	// Schedule edits should take effect immediately, not on the next 60s tick.
	a.applyDesiredMode(false)
}

// effectiveCommercialSettings overlays campaign controls on the existing
// per-spot controls. This keeps old state files valid and lets operators use
// either individual spot controls or named campaign rotations.
func effectiveCommercialSettings(st State) ([]string, map[string]int) {
	blocked := append([]string(nil), st.CommercialBlacklist...)
	weights := make(map[string]int, len(st.CommercialWeights))
	for path, weight := range st.CommercialWeights {
		weights[path] = weight
	}
	for _, campaign := range st.CommercialCampaigns {
		for _, path := range campaign.Files {
			if !campaign.Enabled {
				blocked = append(blocked, path)
				continue
			}
			if campaign.Weight > 1 {
				weights[path] = campaign.Weight
			}
		}
	}
	return blocked, weights
}

// Close shuts down subsystems cleanly.
func (a *App) Close() error {
	a.closing.Store(true)
	if a.cancel != nil {
		a.cancel()
	}
	defer a.Events.Close()
	if a.Button != nil {
		_ = a.Button.Close()
	}
	a.workerWG.Wait()
	// Serialize the final position snapshot and player teardown against EOF,
	// GPIO, and HTTP advances. New advances observe closing and are rejected.
	a.advanceMu.Lock()
	defer a.advanceMu.Unlock()
	// Persist the freshest playhead before systemd terminates mpv. The
	// periodic saver protects normal operation; this shutdown save protects
	// reboot, power-cycle, and handoff tests that happen between ticks.
	a.savePosition()
	if a.Player != nil {
		err := a.Player.Close()
		if a.waitingPath != "" {
			_ = os.Remove(a.waitingPath)
		}
		return err
	}
	if a.waitingPath != "" {
		_ = os.Remove(a.waitingPath)
	}
	return nil
}
