package jerry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/niski84/little-jerrys/internal/clipart"
	"github.com/niski84/little-jerrys/internal/help"
	"github.com/niski84/little-jerrys/internal/jerry/views"
	"github.com/niski84/little-jerrys/internal/network"
	"github.com/niski84/little-jerrys/internal/playlist"
	"github.com/niski84/little-jerrys/internal/schedule"
	"github.com/niski84/little-jerrys/internal/sounds"
	"github.com/niski84/little-jerrys/internal/splash"
	"github.com/niski84/little-jerrys/internal/tmdb"
	"github.com/niski84/little-jerrys/internal/webhook"
	"github.com/niski84/little-jerrys/web"
)

// NewServer wires HTTP routes against the App. Public routes: /login, /static.
// Everything else requires an authenticated session. Playback controls are
// additionally locked while the password is still admin/admin.
func NewServer(app *App) http.Handler {
	mux := http.NewServeMux()

	// Static — public.
	if staticFS, err := web.StaticFS(); err == nil {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
		// Browsers fire /favicon.ico unconditionally before any HTML loads.
		// Serve it from the embedded static dir so we don't 404-spam logs.
		mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "favicon.ico"
			http.FileServer(http.FS(staticFS)).ServeHTTP(w, r2)
		})
	} else {
		fmt.Printf("[jerry] static fs error: %v\n", err)
	}

	// TMDB stills — public, served from the embedded prefetch cache.
	// Returns 404 cleanly when the cache hasn't been populated yet.
	if tmdbFS := app.TMDB.FS(); tmdbFS != nil {
		mux.Handle("GET /tmdb/", http.StripPrefix("/tmdb/", http.FileServer(http.FS(tmdbFS))))
	}

	// Health — public for monitoring.
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "little-jerrys"})
	})

	// Runtime-uploaded soundboard clips. Public-ish (the soundboard page
	// itself is behind auth; this just serves the bytes once you know the
	// URL). Sanitized filename only — no traversal allowed.
	mux.HandleFunc("GET /sounds/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		if name == "" || name == "/" || name == "." || strings.ContainsAny(name, "/\\") {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(app.Cfg.MediaRoot, "sounds", name)
		http.ServeFile(w, r, full)
	})

	// Branding overrides (logo, etc.) live on the USB at <MediaRoot>/branding/.
	// Public so the nav logo loads without a session cookie. Path traversal
	// blocked: only one path segment after /branding/ is allowed.
	mux.HandleFunc("GET /branding/", func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/branding/")
		if rel == "" || strings.Contains(rel, "..") || strings.HasPrefix(rel, "/") {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(app.Cfg.MediaRoot, "branding", filepath.Clean(rel))
		http.ServeFile(w, r, full)
	})

	// Slideshow — public routes so chromium kiosk on the Pi's HDMI-1 can
	// load them without auth (the Pi loads from itself; lock down via host
	// firewall if exposing to other devices).
	mux.HandleFunc("GET /slideshow", func(w http.ResponseWriter, r *http.Request) {
		st := app.Store.Get()
		vm := views.SlideshowVM{
			Enabled: st.SlideshowEnabled,
			Layout:  st.SlideshowLayout,
			SecsPer: st.SlideshowSecsPer,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.Slideshow(vm).Render(r.Context(), w)
	})
	// Lists the bundled slideshow/fan-art images available to the kiosk.
	mux.HandleFunc("GET /api/slideshow/catalog", func(w http.ResponseWriter, r *http.Request) {
		fsRoot, err := web.StaticFS()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		images, _ := clipart.Scan(fsRoot)
		respondJSON(w, http.StatusOK, map[string]interface{}{"images": images})
	})

	// Splash PNG — public so the OSD can be displayed by mpv on the Pi
	// before login. Regenerated on each request so the SSID/admin URL
	// reflect current state.
	mux.HandleFunc("GET /api/splash.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_ = splash.Render(splash.Config{
			SSID:     network.APssid,
			AdminURL: "http://" + network.APaddress,
		}, w)
	})

	// Auth surface.
	// Renders the login form.
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.Login(views.LoginVM{}).Render(r.Context(), w)
	})
	// Verifies credentials and starts an authenticated session.
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		user := r.PostFormValue("user")
		pass := r.PostFormValue("password")
		if !app.Auth.Verify(user, pass) {
			fmt.Printf("[jerry] login FAILED for user=%q from %s\n", user, clientIP(r))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = views.Login(views.LoginVM{Error: "Invalid credentials.", User: user}).Render(r.Context(), w)
			return
		}
		if app.Auth.IsRescue(pass) {
			fmt.Printf("[jerry] login OK (RESCUE) for user=%q from %s\n", user, clientIP(r))
		} else {
			fmt.Printf("[jerry] login OK for user=%q from %s\n", user, clientIP(r))
		}
		app.Auth.Login(w, r)
		if app.Auth.MustChange() {
			http.Redirect(w, r, "/change-password", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	// Clears the session and redirects back to the login page.
	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		app.Auth.Logout(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})

	// Authed surface — wraps a sub-mux with the Require middleware.
	authed := http.NewServeMux()

	// /api/now/preview — streams the currently-playing media file straight
	// to the browser. Range-request support comes free from http.ServeFile,
	// so an HTML5 <video> tag can scrub through it independently of what
	// mpv is doing on Xvfb. This is the dev/test path: see-and-prove the
	// file is real video without relying on VNC into the container.
	authed.HandleFunc("GET /api/now/preview", func(w http.ResponseWriter, r *http.Request) {
		path := app.Player.Status().NowPlaying
		if path == "" {
			http.Error(w, "nothing playing", http.StatusNotFound)
			return
		}
		// Make sure path is inside the configured media root — defense
		// against any future Player implementation that might return a
		// path from elsewhere.
		abs, err := filepath.Abs(path)
		if err != nil {
			http.Error(w, "bad path", http.StatusInternalServerError)
			return
		}
		root, _ := filepath.Abs(app.Cfg.MediaRoot)
		if !strings.HasPrefix(abs, root+string(filepath.Separator)) && abs != root {
			http.Error(w, "out of media root", http.StatusForbidden)
			return
		}
		http.ServeFile(w, r, abs)
	})

	// /api/events/recent — last N events from the in-process ring buffer.
	// Polled by the dashboard's bottom strip every 5s and used as the
	// "what just happened" surface for support emails.
	authed.HandleFunc("GET /api/events/recent", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 || n > 200 {
			n = 20
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"events": app.Events.Recent(n),
		})
	})

	// /api/update/status — returns the pending OTA update notice (if any).
	// Polled by the dashboard banner every 30s. Returns 204 when no update
	// is pending; 200 + JSON when a restart is scheduled.
	authed.HandleFunc("GET /api/update/status", func(w http.ResponseWriter, r *http.Request) {
		notice := app.GetUpdateNotice()
		if notice == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		respondJSON(w, http.StatusOK, notice)
	})

	// /api/status — single source of truth for "what's happening right
	// now." Polled by the dashboard every second to keep the playhead +
	// progress bar live. Also useful for ad-hoc inspection from curl /
	// scripts during development. Auth-required, not behind the
	// LockedDuringDefault gate so post-login spinners can show progress.
	authed.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		st := app.Player.Status()
		var posSecs, durSecs float64
		if d, err := app.Player.Position(); err == nil {
			posSecs = d.Seconds()
		}
		if d, err := app.Player.Duration(); err == nil {
			durSecs = d.Seconds()
		}
		var (
			season, episode int
			title           string
		)
		if s, e, ok := tmdb.SuggestFileMatch(st.NowPlaying); ok {
			season, episode = s, e
			if ep, err := app.TMDB.Episode(s, e); err == nil {
				title = ep.Name
			}
		}
		head := app.Playlist.Peek(8)
		upcoming := make([]map[string]interface{}, 0, len(head))
		for _, it := range head {
			kind := "episode"
			if it.Kind == playlist.KindCommercial {
				kind = "commercial"
			}
			upcoming = append(upcoming, map[string]interface{}{
				"kind":     kind,
				"path":     it.Path,
				"name":     filepath.Base(it.Path),
				"est_secs": it.EstSecs,
			})
		}
		stats := app.Playlist.Stats()
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"now_playing":     st.NowPlaying,
			"now_filename":    filepath.Base(st.NowPlaying),
			"now_title":       title,
			"season":          season,
			"episode":         episode,
			"position_secs":   posSecs,
			"duration_secs":   durSecs,
			"paused":          st.Paused,
			"queue_remaining": stats.QueueRemaining,
			"upcoming":        upcoming,
			"mode":            app.CurrentMode().String(),
		})
	})

	// Renders the form for setting a new admin password.
	authed.HandleFunc("GET /change-password", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.ChangePassword(views.ChangePasswordVM{}).Render(r.Context(), w)
	})
	// Validates and saves a new admin password.
	authed.HandleFunc("POST /change-password", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		pw := r.PostFormValue("password")
		confirm := r.PostFormValue("confirm")
		if pw != confirm {
			renderChangePwd(w, r, "Passwords don't match.")
			return
		}
		if err := app.Auth.ChangePassword(pw); err != nil {
			renderChangePwd(w, r, err.Error())
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	// Renders the main dashboard with now-playing state and the upcoming queue.
	authed.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		var posSecs float64
		if d, err := app.Player.Position(); err == nil {
			posSecs = d.Seconds()
		}
		st := app.Store.Get()
		// Show up to ~30 upcoming items so the page stays fast on phones.
		items := app.Playlist.Peek(30)
		rows := make([]views.QueueRow, 0, len(items))
		for i, it := range items {
			row := views.QueueRow{
				Index:        i,
				Path:         it.Path,
				IsCommercial: it.Kind == playlist.KindCommercial,
				EstSecs:      it.EstSecs,
				PlayCount:      st.PlayCounts[it.Path],
				CompletedCount: st.CompletedCounts[it.Path],
			}
			row.Label = filepath.Base(it.Path)
			rows = append(rows, row)
		}
		vm := views.DashboardVM{
			NowPlaying:      app.Player.Status(),
			Stats:           app.Playlist.Stats(),
			LoopHot:         app.Playlist.LoopGuard(st.LoopGuardThreshold),
			PositionSeconds: posSecs,
			Queue:           rows,
			PlayCounts:      st.PlayCounts,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.Dashboard(vm).Render(r.Context(), w)
	})

	// Renders the per-episode blacklist editor grouped by season.
	authed.HandleFunc("GET /blacklist", func(w http.ResponseWriter, r *http.Request) {
		st := app.Store.Get()
		blocked := map[string]bool{}
		for _, k := range st.Blacklist {
			blocked[k] = true
		}
		vm := views.BlacklistVM{
			BlockedCount: len(st.Blacklist),
			Locked:       app.Auth.MustChange(),
		}
		seasons, _ := app.TMDB.Seasons()
		for _, n := range seasons {
			s, err := app.TMDB.Season(n)
			if err != nil {
				continue
			}
			g := views.BlacklistSeasonGroup{SeasonNumber: n}
			for _, ep := range s.Episodes {
				key := fmt.Sprintf("S%02dE%02d", ep.SeasonNumber, ep.EpisodeNumber)
				g.Episodes = append(g.Episodes, views.BlacklistEntry{
					Season:  ep.SeasonNumber,
					Episode: ep.EpisodeNumber,
					Title:   ep.Name,
					Blocked: blocked[key],
				})
			}
			vm.SeasonGroups = append(vm.SeasonGroups, g)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.Blacklist(vm).Render(r.Context(), w)
	})

	// /weights GET deprecated — weight controls now live on /episodes/{S}/{E}.
	// Redirect any old bookmarks to the episode browser.
	authed.HandleFunc("GET /weights", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/episodes", http.StatusMovedPermanently)
	})

	// Renders the settings page for loop-guard and outbound webhooks.
	authed.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		st := app.Store.Get()
		vm := views.SettingsVM{
			LoopGuardThreshold: st.LoopGuardThreshold,
			WebhookOutbound:    st.WebhookOutbound,
			Locked:             app.Auth.MustChange(),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.Settings(vm).Render(r.Context(), w)
	})

	// Renders the Wi-Fi page showing nearby networks and AP-mode status.
	authed.HandleFunc("GET /network", func(w http.ResponseWriter, r *http.Request) {
		renderNetwork(w, r, app, "", "", "")
	})

	// Renders the schedules page for time-based playback and fan-art windows.
	authed.HandleFunc("GET /schedules", func(w http.ResponseWriter, r *http.Request) {
		renderSchedules(w, r, app, "", "")
	})

	// Renders the weekly and all-time playback summary dashboard.
	authed.HandleFunc("GET /summary", func(w http.ResponseWriter, r *http.Request) {
		renderSummary(w, r, app)
	})

	// Renders the saved-playlists management page.
	authed.HandleFunc("GET /playlists", func(w http.ResponseWriter, r *http.Request) {
		renderPlaylists(w, r, app, "", "")
	})

	// Renders the soundboard page listing uploaded sound clips.
	authed.HandleFunc("GET /soundboard", func(w http.ResponseWriter, r *http.Request) {
		dir := filepath.Join(app.Cfg.MediaRoot, "sounds")
		vm := views.SoundboardVM{
			Clips:    sounds.Scan(dir),
			SoundDir: dir,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.Soundboard(vm).Render(r.Context(), w)
	})

	// Redirects to the first available episode in the episode browser.
	authed.HandleFunc("GET /episodes", func(w http.ResponseWriter, r *http.Request) {
		if seasons, _ := app.TMDB.Seasons(); len(seasons) > 0 {
			if s, err := app.TMDB.Season(seasons[0]); err == nil && len(s.Episodes) > 0 {
				http.Redirect(w, r, fmt.Sprintf("/episodes/%d/%d", seasons[0], s.Episodes[0].EpisodeNumber), http.StatusSeeOther)
				return
			}
		}
		renderEpisodes(w, r, app, 0, 0)
	})
	// Browses a season's episodes without a detail panel selected.
	authed.HandleFunc("GET /episodes/{season}", func(w http.ResponseWriter, r *http.Request) {
		s, _ := strconv.Atoi(r.PathValue("season"))
		renderEpisodes(w, r, app, s, 0)
	})
	// Shows a single episode's detail, weight, and blacklist controls.
	authed.HandleFunc("GET /episodes/{season}/{episode}", func(w http.ResponseWriter, r *http.Request) {
		s, _ := strconv.Atoi(r.PathValue("season"))
		e, _ := strconv.Atoi(r.PathValue("episode"))
		renderEpisodes(w, r, app, s, e)
	})

	// Lands on the first help topic in the index.
	authed.HandleFunc("GET /help", func(w http.ResponseWriter, r *http.Request) {
		renderHelp(w, r, "")
	})
	// Renders a single help topic by its slug.
	authed.HandleFunc("GET /help/{slug}", func(w http.ResponseWriter, r *http.Request) {
		renderHelp(w, r, r.PathValue("slug"))
	})

	// Renders the commercial-library page with weights and break scheduling.
	authed.HandleFunc("GET /commercials", func(w http.ResponseWriter, r *http.Request) {
		st := app.Store.Get()
		blocked := map[string]bool{}
		for _, p := range st.CommercialBlacklist {
			blocked[p] = true
		}
		vm := views.CommercialsVM{
			Catalog:             app.Playlist.CommercialCatalog(),
			Blocked:             blocked,
			Weights:             st.CommercialWeights,
			Enabled:             st.CommercialsEnabled,
			BreakAfterMinutes:   st.BreakAfterMinutes,
			CommercialsPerBreak: st.CommercialsPerBreak,
			Locked:              app.Auth.MustChange(),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.Commercials(vm).Render(r.Context(), w)
	})

	// State-mutating handlers (gated behind LockedDuringDefault).
	playback := http.NewServeMux()
	// Resumes playback of the current track.
	playback.HandleFunc("POST /api/play", func(w http.ResponseWriter, r *http.Request) {
		if err := app.Player.Resume(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		app.Webhook.Fire(webhook.Event{Type: "play"})
		w.WriteHeader(http.StatusNoContent)
	})
	// Pauses the current track.
	playback.HandleFunc("POST /api/pause", func(w http.ResponseWriter, r *http.Request) {
		if err := app.Player.Pause(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		app.Webhook.Fire(webhook.Event{Type: "pause"})
		w.WriteHeader(http.StatusNoContent)
	})
	// Seeks the current track to a given position in seconds.
	playback.HandleFunc("POST /api/seek", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// Accept secs from form body or query param so curl + UI both work.
		raw := r.PostFormValue("secs")
		if raw == "" {
			raw = r.URL.Query().Get("secs")
		}
		secs, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			http.Error(w, "missing or invalid 'secs' (float seconds)", http.StatusBadRequest)
			return
		}
		if err := app.Player.Seek(time.Duration(secs * float64(time.Second))); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// Skips to the next item in the queue.
	playback.HandleFunc("POST /api/skip", func(w http.ResponseWriter, r *http.Request) {
		if err := app.PlayNext(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		app.Webhook.Fire(webhook.Event{Type: "skip"})
		w.WriteHeader(http.StatusNoContent)
	})
	// Simulates a physical button press in builds with a button simulator.
	playback.HandleFunc("POST /api/button/press", func(w http.ResponseWriter, r *http.Request) {
		if sim, ok := app.Button.(interface{ Simulate() }); ok {
			sim.Simulate()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "physical button only — simulator unavailable on this build", http.StatusBadRequest)
	})
	// Per-episode controls — both keyed by SNNENN reference. These replace
	// the old path-based /blacklist/toggle and /weights/save endpoints.
	playback.HandleFunc("POST /episodes/{season}/{episode}/weight", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s, _ := strconv.Atoi(r.PathValue("season"))
		e, _ := strconv.Atoi(r.PathValue("episode"))
		weight, _ := strconv.Atoi(r.PostFormValue("weight"))
		if s < 1 || e < 1 {
			http.Error(w, "bad episode", http.StatusBadRequest)
			return
		}
		key := fmt.Sprintf("S%02dE%02d", s, e)
		_ = app.Store.Update(func(st *State) {
			if st.Weights == nil {
				st.Weights = map[string]int{}
			}
			if weight <= 1 {
				delete(st.Weights, key)
			} else {
				st.Weights[key] = weight
			}
		})
		app.ApplyState()
		http.Redirect(w, r, fmt.Sprintf("/episodes/%d/%d", s, e), http.StatusSeeOther)
	})
	// Toggles an episode's blacklist state by season/episode.
	playback.HandleFunc("POST /episodes/{season}/{episode}/blocked", func(w http.ResponseWriter, r *http.Request) {
		s, _ := strconv.Atoi(r.PathValue("season"))
		e, _ := strconv.Atoi(r.PathValue("episode"))
		if s < 1 || e < 1 {
			http.Error(w, "bad episode", http.StatusBadRequest)
			return
		}
		key := fmt.Sprintf("S%02dE%02d", s, e)
		_ = app.Store.Update(func(st *State) {
			for i, k := range st.Blacklist {
				if k == key {
					st.Blacklist = append(st.Blacklist[:i], st.Blacklist[i+1:]...)
					return
				}
			}
			st.Blacklist = append(st.Blacklist, key)
		})
		app.ApplyState()
		// If the click came from /blacklist, redirect there; otherwise
		// back to the episode detail.
		ref := r.Header.Get("Referer")
		if strings.Contains(ref, "/blacklist") {
			http.Redirect(w, r, "/blacklist", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/episodes/%d/%d", s, e), http.StatusSeeOther)
	})
	// Saves loop-guard threshold and outbound webhook URLs.
	playback.HandleFunc("POST /settings/save", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		threshold, _ := strconv.Atoi(r.PostFormValue("loop_threshold"))
		if threshold < 1 {
			threshold = 3
		}
		urls := []string{}
		for _, line := range strings.Split(r.PostFormValue("webhook_outbound"), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				urls = append(urls, line)
			}
		}
		_ = app.Store.Update(func(s *State) {
			s.LoopGuardThreshold = threshold
			s.WebhookOutbound = urls
		})
		app.ApplyState()
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
	})

	// Saves commercial-break scheduling settings.
	playback.HandleFunc("POST /commercials/schedule", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		enabled := r.PostFormValue("enabled") == "1"
		breakAfter, _ := strconv.Atoi(r.PostFormValue("break_after"))
		perBreak, _ := strconv.Atoi(r.PostFormValue("per_break"))
		if breakAfter < 1 {
			breakAfter = 22
		}
		if perBreak < 1 {
			perBreak = 1
		}
		_ = app.Store.Update(func(s *State) {
			s.CommercialsEnabled = enabled
			s.BreakAfterMinutes = breakAfter
			s.CommercialsPerBreak = perBreak
		})
		app.ApplyState()
		http.Redirect(w, r, "/commercials", http.StatusSeeOther)
	})

	// Queue manipulation — used by the dashboard's playlist editor.
	// Rescans the media root for new files without a reboot.
	playback.HandleFunc("POST /api/queue/rescan", func(w http.ResponseWriter, r *http.Request) {
		// USB hot-swap: Brian drops new files in, hits this from the admin UI,
		// playlist sees the new files without a reboot.
		if err := app.Playlist.Scan(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		app.Playlist.Regenerate()
		app.SnapshotQueue()
		w.WriteHeader(http.StatusNoContent)
	})
	// Reshuffles the queue and jumps to the new head track.
	playback.HandleFunc("POST /api/queue/regenerate", func(w http.ResponseWriter, r *http.Request) {
		// Regenerate is the user saying "scrap whatever's playing and start
		// over." So we shuffle the queue, persist it, AND interrupt the
		// current track to start playing the new head. Without the
		// PlayNext, the dashboard's Up-Next list would update but mpv
		// would keep playing the old track (or sit idle), making the UI
		// feel disconnected from what's on screen.
		app.Playlist.Regenerate()
		app.SnapshotQueue()
		if err := app.PlayNext(); err != nil {
			app.Events.Warnf("playback", "regenerate: PlayNext failed: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// Removes a queued item at the given index.
	playback.HandleFunc("POST /api/queue/remove", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idx, err := strconv.Atoi(r.PostFormValue("index"))
		if err != nil {
			http.Error(w, "bad index", http.StatusBadRequest)
			return
		}
		if err := app.Playlist.RemoveAt(idx); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		app.SnapshotQueue()
		w.WriteHeader(http.StatusNoContent)
	})
	// Moves a queued item from one index to another.
	playback.HandleFunc("POST /api/queue/reorder", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		from, err1 := strconv.Atoi(r.PostFormValue("from"))
		to, err2 := strconv.Atoi(r.PostFormValue("to"))
		if err1 != nil || err2 != nil {
			http.Error(w, "bad from/to", http.StatusBadRequest)
			return
		}
		if err := app.Playlist.ReorderAt(from, to); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		app.SnapshotQueue()
		w.WriteHeader(http.StatusNoContent)
	})

	// Creates a new time-based playback schedule.
	playback.HandleFunc("POST /schedules/add", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s := schedule.Schedule{
			ID:        shortID(),
			Name:      strings.TrimSpace(r.PostFormValue("name")),
			StartTime: r.PostFormValue("start_time"),
			EndTime:   r.PostFormValue("end_time"),
			Enabled:   true,
		}
		for _, v := range r.PostForm["days"] {
			n, err := strconv.Atoi(v)
			if err == nil {
				s.Days = append(s.Days, n)
			}
		}
		if err := s.Validate(); err != nil {
			renderSchedules(w, r, app, err.Error(), "")
			return
		}
		_ = app.Store.Update(func(st *State) {
			st.Schedules = append(st.Schedules, s)
		})
		app.ApplyState()
		http.Redirect(w, r, "/schedules", http.StatusSeeOther)
	})
	// Enables or disables a schedule by ID.
	playback.HandleFunc("POST /schedules/toggle", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.PostFormValue("id")
		_ = app.Store.Update(func(st *State) {
			for i := range st.Schedules {
				if st.Schedules[i].ID == id {
					st.Schedules[i].Enabled = !st.Schedules[i].Enabled
					return
				}
			}
		})
		app.ApplyState()
		http.Redirect(w, r, "/schedules", http.StatusSeeOther)
	})
	// Deletes a schedule by ID.
	playback.HandleFunc("POST /schedules/delete", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.PostFormValue("id")
		_ = app.Store.Update(func(st *State) {
			out := st.Schedules[:0]
			for _, s := range st.Schedules {
				if s.ID != id {
					out = append(out, s)
				}
			}
			st.Schedules = out
		})
		app.ApplyState()
		http.Redirect(w, r, "/schedules", http.StatusSeeOther)
	})
	// Playlists CRUD.
	// Creates a new named playlist.
	playback.HandleFunc("POST /playlists/create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		name := strings.TrimSpace(r.PostFormValue("name"))
		if name == "" {
			renderPlaylists(w, r, app, "name is required", "")
			return
		}
		_ = app.Store.Update(func(s *State) {
			s.Playlists = append(s.Playlists, Playlist{
				ID:       shortID(),
				Name:     name,
				Episodes: []EpisodeRef{},
				Created:  time.Now().Format("2006-01-02"),
			})
		})
		http.Redirect(w, r, "/playlists", http.StatusSeeOther)
	})
	// Deletes a playlist by ID.
	playback.HandleFunc("POST /playlists/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_ = app.Store.Update(func(s *State) {
			out := s.Playlists[:0]
			for _, p := range s.Playlists {
				if p.ID != id {
					out = append(out, p)
				}
			}
			s.Playlists = out
		})
		http.Redirect(w, r, "/playlists", http.StatusSeeOther)
	})
	// Loads a playlist's episodes into the queue and starts playing.
	playback.HandleFunc("POST /playlists/{id}/play", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		st := app.Store.Get()
		var pl *Playlist
		for i := range st.Playlists {
			if st.Playlists[i].ID == id {
				pl = &st.Playlists[i]
				break
			}
		}
		if pl == nil {
			http.Error(w, "playlist not found", http.StatusNotFound)
			return
		}
		// Resolve each episode ref to a media file path. Skip unresolved
		// (no matching .mp4 yet) — they'll show in the UI as "no file".
		var items []playlist.Item
		for _, ref := range pl.Episodes {
			if path := app.Playlist.ResolvePathForEpisode(ref.Season, ref.Episode); path != "" {
				items = append(items, playlist.Item{
					Kind: playlist.KindEpisode, Path: path, EstSecs: 22 * 60,
				})
			}
		}
		app.Playlist.LoadQueue(items)
		_ = app.PlayNext()
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	// Removes an episode from a playlist.
	playback.HandleFunc("POST /playlists/{id}/remove", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.PathValue("id")
		s, _ := strconv.Atoi(r.PostFormValue("season"))
		e, _ := strconv.Atoi(r.PostFormValue("episode"))
		_ = app.Store.Update(func(st *State) {
			for i := range st.Playlists {
				if st.Playlists[i].ID != id {
					continue
				}
				out := st.Playlists[i].Episodes[:0]
				for _, ep := range st.Playlists[i].Episodes {
					if ep.Season == s && ep.Episode == e {
						continue
					}
					out = append(out, ep)
				}
				st.Playlists[i].Episodes = out
			}
		})
		http.Redirect(w, r, "/playlists", http.StatusSeeOther)
	})
	// Adds an episode to a playlist, skipping duplicates.
	playback.HandleFunc("POST /playlists/add-episode", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.PostFormValue("playlist_id")
		s, _ := strconv.Atoi(r.PostFormValue("season"))
		e, _ := strconv.Atoi(r.PostFormValue("episode"))
		_ = app.Store.Update(func(st *State) {
			for i := range st.Playlists {
				if st.Playlists[i].ID != id {
					continue
				}
				// Skip duplicates.
				for _, ep := range st.Playlists[i].Episodes {
					if ep.Season == s && ep.Episode == e {
						return
					}
				}
				st.Playlists[i].Episodes = append(st.Playlists[i].Episodes,
					EpisodeRef{Season: s, Episode: e})
			}
		})
		http.Redirect(w, r, fmt.Sprintf("/episodes/%d/%d", s, e), http.StatusSeeOther)
	})

	// Saves slideshow kiosk settings (enabled, layout, seconds per image).
	playback.HandleFunc("POST /schedules/slideshow", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		enabled := r.PostFormValue("enabled") == "1"
		layout, _ := strconv.Atoi(r.PostFormValue("layout"))
		secs, _ := strconv.Atoi(r.PostFormValue("secs_per"))
		// Constrain to the three layouts the kiosk page knows about.
		if layout != 1 && layout != 4 && layout != 16 {
			layout = 4
		}
		if secs < 3 {
			secs = 12
		}
		_ = app.Store.Update(func(st *State) {
			st.SlideshowEnabled = enabled
			st.SlideshowLayout = layout
			st.SlideshowSecsPer = secs
		})
		http.Redirect(w, r, "/schedules", http.StatusSeeOther)
	})

	// Saves fan-art screensaver settings (enabled, seconds per image).
	playback.HandleFunc("POST /schedules/fan-art", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		enabled := r.PostFormValue("enabled") == "1"
		secs, _ := strconv.Atoi(r.PostFormValue("secs_per"))
		if secs < 3 {
			secs = 8
		}
		_ = app.Store.Update(func(st *State) {
			st.FanArtEnabled = enabled
			st.FanArtSecsPerImage = secs
		})
		app.ApplyState()
		http.Redirect(w, r, "/schedules", http.StatusSeeOther)
	})

	// Rescans for nearby Wi-Fi networks and re-renders the page.
	playback.HandleFunc("POST /network/scan", func(w http.ResponseWriter, r *http.Request) {
		// Scan happens inside renderNetwork — re-rendering the page is the response.
		renderNetwork(w, r, app, "", "", "")
	})
	// Joins a Wi-Fi network by SSID and password, disabling AP mode.
	playback.HandleFunc("POST /network/join", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ssid := r.PostFormValue("ssid")
		pw := r.PostFormValue("password")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := app.Network.JoinNetwork(ctx, ssid, pw); err != nil {
			renderNetwork(w, r, app, "", err.Error(), "")
			return
		}
		renderNetwork(w, r, app, "", "", "Joined "+ssid+". AP mode disabled.")
	})
	// Starts the device's Wi-Fi access-point mode.
	playback.HandleFunc("POST /network/ap/start", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := app.Network.EnsureAPProfile(ctx); err == nil {
			_ = app.Network.StartAP(ctx)
		}
		http.Redirect(w, r, "/network", http.StatusSeeOther)
	})
	// Stops the device's Wi-Fi access-point mode.
	playback.HandleFunc("POST /network/ap/stop", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		_ = app.Network.StopAP(ctx)
		http.Redirect(w, r, "/network", http.StatusSeeOther)
	})

	// Saves per-commercial weights and blacklist from the library form.
	playback.HandleFunc("POST /commercials/library", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		newWeights := map[string]int{}
		var newBlacklist []string
		for key, vals := range r.PostForm {
			if len(vals) == 0 {
				continue
			}
			switch {
			case strings.HasPrefix(key, "cw:"):
				path := strings.TrimPrefix(key, "cw:")
				n, err := strconv.Atoi(vals[0])
				if err != nil || n < 1 {
					continue
				}
				if n > 1 {
					newWeights[path] = n
				}
			case strings.HasPrefix(key, "cb:"):
				if vals[0] == "1" {
					newBlacklist = append(newBlacklist, strings.TrimPrefix(key, "cb:"))
				}
			}
		}
		_ = app.Store.Update(func(s *State) {
			s.CommercialWeights = newWeights
			s.CommercialBlacklist = newBlacklist
		})
		app.ApplyState()
		http.Redirect(w, r, "/commercials", http.StatusSeeOther)
	})

	// Compose: session gate → password-change gate → UI (with mutation sub-mux
	// additionally locked while the default password is still in place).
	authed.Handle("/", app.Auth.LockedDuringDefault(playback))
	mux.Handle("/", app.Auth.Require(app.Auth.RequirePasswordChanged(authed)))

	return mux
}

func renderChangePwd(w http.ResponseWriter, r *http.Request, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = views.ChangePassword(views.ChangePasswordVM{Error: msg}).Render(r.Context(), w)
}

// renderSchedules renders the schedules admin page. addErr surfaces a
// validation message on add failures; savedMsg surfaces a success banner.
func renderSchedules(w http.ResponseWriter, r *http.Request, app *App, addErr, savedMsg string) {
	st := app.Store.Get()
	var fanArtCount int
	if fsRoot, err := web.StaticFS(); err == nil {
		if list, _ := clipart.Scan(fsRoot); list != nil {
			fanArtCount = len(list)
		}
	}
	vm := views.SchedulesVM{
		Schedules:          st.Schedules,
		CurrentMode:        app.CurrentMode().String(),
		FanArtEnabled:      st.FanArtEnabled,
		FanArtSecsPerImage: st.FanArtSecsPerImage,
		FanArtCount:        fanArtCount,
		AddError:           addErr,
		Locked:             app.Auth.MustChange(),
		SavedMessage:       savedMsg,
		SlideshowEnabled:   st.SlideshowEnabled,
		SlideshowLayout:    st.SlideshowLayout,
		SlideshowSecsPer:   st.SlideshowSecsPer,
		SlideshowCount:     fanArtCount,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Schedules(vm).Render(r.Context(), w)
}

// shortID returns a URL-safe 8-char ID for new schedule entries.
func shortID() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = alphabet[time.Now().UnixNano()%int64(len(alphabet))]
		time.Sleep(time.Microsecond) // bump the nanosecond clock so each char varies
	}
	return string(b)
}

// renderSummary builds the weekly playback dashboard. ThisWeek is computed
// from live snapshot deltas; LastWeek is the frozen rollup written at the
// most recent Monday rollover. All-time is just the lifetime totals.
func renderSummary(w http.ResponseWriter, r *http.Request, app *App) {
	st := app.Store.Get()
	thisWeek := app.ComputeCurrentWeekRollup()

	vm := views.SummaryVM{
		WeekStart: st.WeekStart,
		ThisWeek: views.WeekCardVM{
			Label:             weekLabel(st.WeekStart),
			EpisodesStarted:   thisWeek.EpisodesStarted,
			EpisodesCompleted: thisWeek.EpisodesCompleted,
			UniqueEpisodes:    thisWeek.UniqueEpisodes,
			MinutesPlayed:     thisWeek.MinutesPlayed,
			MinutesPaused:     thisWeek.MinutesPaused,
			Top:               topWatched(app, thisWeek.PerEpisode, 5),
		},
	}
	if st.LastWeekRollup != nil {
		lw := st.LastWeekRollup
		vm.LastWeek = &views.WeekCardVM{
			Label:             weekLabel(lw.Start),
			EpisodesStarted:   lw.EpisodesStarted,
			EpisodesCompleted: lw.EpisodesCompleted,
			UniqueEpisodes:    lw.UniqueEpisodes,
			MinutesPlayed:     lw.MinutesPlayed,
			MinutesPaused:     lw.MinutesPaused,
			Top:               topWatched(app, lw.PerEpisode, 5),
		}
	}

	// All-time: lifetime totals computed straight from state.
	allStarted := 0
	for _, c := range st.PlayCounts {
		allStarted += c
	}
	allCompleted := 0
	uniqueCompleted := 0
	for _, c := range st.CompletedCounts {
		if c > 0 {
			uniqueCompleted++
		}
		allCompleted += c
	}
	vm.AllTime = views.AllTimeVM{
		EpisodesStarted:   allStarted,
		EpisodesCompleted: allCompleted,
		UniqueEpisodes:    uniqueCompleted,
		MinutesPlayed:     st.MinutesPlayed,
		MinutesPaused:     st.MinutesPaused,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Summary(vm).Render(r.Context(), w)
}

// weekLabel formats an ISO date "YYYY-MM-DD" as a human-readable date
// range "Mmm D – Mmm D" covering Monday through Sunday.
func weekLabel(isoStart string) string {
	if isoStart == "" {
		return "(uninitialized)"
	}
	t, err := time.Parse("2006-01-02", isoStart)
	if err != nil {
		return isoStart
	}
	end := t.AddDate(0, 0, 6)
	if t.Month() == end.Month() {
		return fmt.Sprintf("%s %d–%d", t.Format("Jan"), t.Day(), end.Day())
	}
	return fmt.Sprintf("%s %d – %s %d", t.Format("Jan"), t.Day(), end.Format("Jan"), end.Day())
}

// topWatched picks the top N entries from a path → completed-count map,
// resolves TMDB titles where possible, and returns a sorted slice.
func topWatched(app *App, perEp map[string]int, n int) []views.TopEntry {
	type kv struct {
		path  string
		count int
	}
	pairs := make([]kv, 0, len(perEp))
	for k, v := range perEp {
		if v > 0 {
			pairs = append(pairs, kv{k, v})
		}
	}
	// Simple n-largest: sort desc by count.
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].count > pairs[i].count {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	if n > len(pairs) {
		n = len(pairs)
	}
	out := make([]views.TopEntry, 0, n)
	for i := 0; i < n; i++ {
		entry := views.TopEntry{
			Path:  pairs[i].path,
			Label: filepath.Base(pairs[i].path),
			Count: pairs[i].count,
		}
		if s, e, ok := tmdb.SuggestFileMatch(pairs[i].path); ok {
			entry.Season = s
			entry.Episode = e
			if ep, err := app.TMDB.Episode(s, e); err == nil {
				entry.Label = ep.Name
			}
		}
		out = append(out, entry)
	}
	return out
}

// renderPlaylists hydrates the saved playlists with TMDB titles for each
// episode reference, so the page reads naturally instead of showing bare
// S/E numbers.
func renderPlaylists(w http.ResponseWriter, r *http.Request, app *App, createErr, savedMsg string) {
	st := app.Store.Get()
	rows := make([]views.PlaylistVM, 0, len(st.Playlists))
	for _, p := range st.Playlists {
		eps := make([]views.PlaylistEpisodeVM, 0, len(p.Episodes))
		for _, ref := range p.Episodes {
			row := views.PlaylistEpisodeVM{Season: ref.Season, Episode: ref.Episode}
			if ep, err := app.TMDB.Episode(ref.Season, ref.Episode); err == nil {
				row.Title = ep.Name
			}
			row.Resolved = app.Playlist.ResolvePathForEpisode(ref.Season, ref.Episode) != ""
			eps = append(eps, row)
		}
		rows = append(rows, views.PlaylistVM{
			ID:       p.ID,
			Name:     p.Name,
			Created:  p.Created,
			Episodes: eps,
		})
	}
	vm := views.PlaylistsPageVM{
		Playlists:    rows,
		CreateError:  createErr,
		SavedMessage: savedMsg,
		Locked:       app.Auth.MustChange(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Playlists(vm).Render(r.Context(), w)
}

// playlistRefsForEpisode is the trimmed list (id+name only) used by the
// "Add to Playlist" widget on episode detail.
func playlistRefsForEpisode(app *App) []views.PlaylistVM {
	st := app.Store.Get()
	out := make([]views.PlaylistVM, 0, len(st.Playlists))
	for _, p := range st.Playlists {
		out = append(out, views.PlaylistVM{ID: p.ID, Name: p.Name})
	}
	return out
}

// renderEpisodes is the shared render path for the /episodes browse UI.
// season=0 → default to first available; episode=0 → no detail panel.
func renderEpisodes(w http.ResponseWriter, r *http.Request, app *App, season, episode int) {
	var vm views.EpisodesVM
	show, err := app.TMDB.Show()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	vm.Show = show

	seasons, _ := app.TMDB.Seasons()
	vm.SeasonNumbers = seasons
	if season == 0 && len(seasons) > 0 {
		season = seasons[0]
	}
	if season > 0 {
		s, err := app.TMDB.Season(season)
		if err == nil {
			vm.ActiveSeason = s
		}
	}
	st := app.Store.Get()
	vm.Locked = app.Auth.MustChange()
	blockedKeys := map[string]bool{}
	for _, k := range st.Blacklist {
		blockedKeys[k] = true
	}

	if episode > 0 && vm.ActiveSeason != nil {
		for i := range vm.ActiveSeason.Episodes {
			if vm.ActiveSeason.Episodes[i].EpisodeNumber == episode {
				vm.ActiveEpisode = &vm.ActiveSeason.Episodes[i]
				vm.StillURL = app.TMDB.StillURL(season, episode)
				vm.AddToPlaylist = &views.AddToPlaylistVM{
					Season:    season,
					Episode:   episode,
					Playlists: playlistRefsForEpisode(app),
					Locked:    vm.Locked,
				}
				key := fmt.Sprintf("S%02dE%02d", season, episode)
				vm.ActiveWeight = st.Weights[key]
				if vm.ActiveWeight < 1 {
					vm.ActiveWeight = 1
				}
				vm.ActiveBlocked = blockedKeys[key]
				break
			}
		}
	}

	// Compute summary stats — overall and per-season — by walking every
	// known season's episode list. Lets the UI surface "X weighted, Y
	// blocked" + a per-tab badge + an expandable list of all changes.
	vm.SeasonStats = map[int]views.SeasonModSummary{}
	for _, n := range seasons {
		s, err := app.TMDB.Season(n)
		if err != nil {
			continue
		}
		var stat views.SeasonModSummary
		for _, ep := range s.Episodes {
			key := fmt.Sprintf("S%02dE%02d", ep.SeasonNumber, ep.EpisodeNumber)
			weighted := st.Weights[key] > 1
			blocked := blockedKeys[key]
			if weighted {
				stat.Weighted++
				vm.Summary.Weighted++
			}
			if blocked {
				stat.Blocked++
				vm.Summary.Blocked++
			}
			if weighted || blocked {
				vm.SummaryDetail = append(vm.SummaryDetail, views.EpisodeModRef{
					Season:  ep.SeasonNumber,
					Episode: ep.EpisodeNumber,
					Title:   ep.Name,
					Weight:  st.Weights[key],
					Blocked: blocked,
				})
			}
		}
		vm.SeasonStats[n] = stat
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Episodes(vm).Render(r.Context(), w)
}

// renderHelp resolves a topic slug to embedded markdown and renders the page.
// Empty slug → land on the first topic in the index. Unknown slug → 404 vm.
func renderHelp(w http.ResponseWriter, r *http.Request, slug string) {
	topics, err := help.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if slug == "" && len(topics) > 0 {
		http.Redirect(w, r, "/help/"+topics[0].Slug, http.StatusSeeOther)
		return
	}
	vm := views.HelpVM{Topics: topics, ActiveSlug: slug}
	htmlOut, title, err := help.Render(slug)
	if err != nil {
		vm.NotFound = true
		vm.Title = "Not found"
	} else {
		vm.ContentHTML = htmlOut
		vm.Title = title
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Help(vm).Render(r.Context(), w)
}

// renderNetwork is the shared render path for /network views — handles GET,
// rescan, and post-join feedback. Scan failures surface as a banner, not a
// 500, so the page still works in degraded states.
func renderNetwork(w http.ResponseWriter, r *http.Request, app *App, scanErr, joinErr, joinOK string) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	vm := views.NetworkVM{
		Available:    app.Network.Available(),
		APssid:       network.APssid,
		APaddress:    network.APaddress,
		LANAddresses: app.Network.LANAddresses(),
		Locked:       app.Auth.MustChange(),
		ScanError:    scanErr,
		JoinError:    joinErr,
		JoinSuccess:  joinOK,
	}
	if vm.Available {
		vm.HasInternet = app.Network.HasInternet(ctx)
		vm.APMode = app.Network.APActive(ctx)
		ssids, err := app.Network.Scan(ctx)
		if err != nil {
			vm.ScanError = err.Error()
		} else {
			for _, s := range ssids {
				vm.Networks = append(vm.Networks, views.ScannedNetwork{
					SSID: s.Name, Signal: s.Signal, InUse: s.InUse,
				})
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Network(vm).Render(r.Context(), w)
}

// clientIP returns the best-guess remote IP for log lines. Prefers
// X-Forwarded-For (in case we ever sit behind nginx) over RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return host
}

func respondJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Printf("[jerry] encode error: %v\n", err)
	}
}
