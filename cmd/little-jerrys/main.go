package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/niski84/little-jerrys/internal/jerry"
	"github.com/niski84/little-jerrys/internal/tmdb"
	"github.com/niski84/little-jerrys/internal/updater"
)

// Version is set at build time via -ldflags "-X main.Version=v0.1.0".
// A dev build without ldflags gets "v0.0.0-dev" which is never ≥ any real release.
var Version = "v0.0.0-dev"

func main() {
	resetPassword := flag.Bool("reset-password", false, "wipe the admin password back to admin/admin and exit")
	prefetchTMDB := flag.Bool("prefetch-tmdb", false, "download Seinfeld metadata + stills from TMDB into data/tmdb/, then exit")
	tmdbKey := flag.String("tmdb-key", "", "TMDB API key (overrides JERRY_TMDB_API_KEY env)")
	flag.Parse()

	cfg := jerry.LoadConfig()

	if *resetPassword {
		if err := jerry.ResetPassword(cfg.StatePath); err != nil {
			log.Fatalf("[little-jerrys] reset-password: %v", err)
		}
		fmt.Printf("[little-jerrys] admin password reset — login is admin/admin again\n")
		return
	}

	if *prefetchTMDB {
		key := *tmdbKey
		if key == "" {
			key = os.Getenv("JERRY_TMDB_API_KEY")
		}
		if err := tmdb.Prefetch(context.Background(), tmdb.SeinfeldShowID, key, "internal/tmdb/data"); err != nil {
			log.Fatalf("[little-jerrys] prefetch-tmdb: %v", err)
		}
		return
	}

	// TMDB cache is package-level (internal/tmdb/embed.go) — nothing to wire here.

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	app, err := jerry.NewApp(ctx, cfg)
	if err != nil {
		log.Fatalf("[little-jerrys] init: %v", err)
	}
	defer app.Close()

	// Kick off playback. If a saved position exists, mpv resumes there;
	// otherwise the random rotation starts fresh. If there's no media yet,
	// this fails loudly — that's fine in dev.
	if err := app.PlayCurrent(); err != nil {
		fmt.Printf("[little-jerrys] initial play warning: %v\n", err)
	}

	// Auto-updater: checks GitHub Releases on boot (after 2 min) and once
	// more each day; applies during the safe window (default 02:00–04:00).
	if cfg.AutoUpdate {
		u := updater.New(
			Version,
			cfg.UpdateWindowFrom,
			cfg.UpdateWindowTo,
			2*time.Minute,
			10*time.Minute, // countdown warning before restart
			func(msg string) { fmt.Printf("[updater] %s\n", msg) },
			func(newVersion, releaseURL string, applyAt time.Time) {
				// Store notice for the web dashboard banner.
				app.SetUpdateNotice(&jerry.UpdateNotice{
					Version:    newVersion,
					ReleaseURL: releaseURL,
					ApplyAt:    applyAt,
				})
				// Show OSD on the TV so staff aren't surprised.
				msg := fmt.Sprintf("Maintenance restart in 10 min — Little Jerry's %s", newVersion)
				if err := app.Player.ShowOSD(msg, 30*time.Second); err != nil {
					fmt.Printf("[updater] OSD warning: %v\n", err)
				}
				// Repeat OSD at 5 min and 1 min marks.
				go func() {
					time.Sleep(5 * time.Minute)
					_ = app.Player.ShowOSD(fmt.Sprintf("Maintenance restart in 5 min — Little Jerry's %s", newVersion), 20*time.Second)
					time.Sleep(4 * time.Minute)
					_ = app.Player.ShowOSD("Maintenance restart in 1 min…", 60*time.Second)
				}()
			},
		)
		go u.Run(ctx)
	}

	srv := jerry.NewServer(app)
	addr := ":" + cfg.Port
	fmt.Printf("[little-jerrys] ready at http://localhost:%s\n", cfg.Port)

	httpSrv := &http.Server{Addr: addr, Handler: srv}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[little-jerrys] http: %v", err)
	}
}
