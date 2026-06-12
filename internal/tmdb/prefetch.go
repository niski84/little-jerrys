package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Prefetch downloads the show, every season, and one still image per
// episode into outDir. Existing files are skipped (idempotent — re-run
// is cheap). Polite to TMDB: ~50ms gap between requests so we never
// approach the 50/sec limit.
//
// outDir is typically <project>/data/tmdb. After prefetch, rebuild the
// binary so go:embed picks up the new files.
func Prefetch(ctx context.Context, showID int, apiKey, outDir string) error {
	if apiKey == "" {
		return fmt.Errorf("apiKey is empty — set JERRY_TMDB_API_KEY or pass --tmdb-key")
	}
	c := NewClient(apiKey)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	// Show
	fmt.Printf("[tmdb] fetching show %d…\n", showID)
	show, err := c.FetchShow(ctx, showID)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "show.json"), show); err != nil {
		return err
	}
	fmt.Printf("[tmdb]   %s · %d seasons · %d episodes\n", show.Name, show.NumSeasons, show.NumEpisodes)

	// Seasons + episodes
	stillsDownloaded := 0
	stillsSkipped := 0
	stillsFailed := 0
	for n := 1; n <= show.NumSeasons; n++ {
		fmt.Printf("[tmdb] season %d…\n", n)
		season, err := c.FetchSeason(ctx, showID, n)
		if err != nil {
			fmt.Printf("[tmdb]   skipping season %d: %v\n", n, err)
			continue
		}
		seasonDir := filepath.Join(outDir, "seasons", fmt.Sprintf("%02d", n))
		if err := os.MkdirAll(filepath.Join(seasonDir, "episodes"), 0o755); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(seasonDir, "season.json"), season); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)

		for _, ep := range season.Episodes {
			if ep.StillPath == "" {
				continue
			}
			out := filepath.Join(seasonDir, "episodes", fmt.Sprintf("%02d.jpg", ep.EpisodeNumber))
			if _, err := os.Stat(out); err == nil {
				stillsSkipped++
				continue
			}
			body, err := c.FetchImage(ctx, ep.StillPath)
			if err != nil {
				fmt.Printf("[tmdb]   ! S%02dE%02d still: %v\n", n, ep.EpisodeNumber, err)
				stillsFailed++
				continue
			}
			if err := os.WriteFile(out, body, 0o644); err != nil {
				return fmt.Errorf("write still: %w", err)
			}
			stillsDownloaded++
			time.Sleep(50 * time.Millisecond)
		}
	}

	fmt.Printf("[tmdb] done. stills downloaded=%d skipped=%d failed=%d → %s\n",
		stillsDownloaded, stillsSkipped, stillsFailed, outDir)
	fmt.Printf("[tmdb] rebuild the binary to bake the cache in: bash scripts/compile.sh\n")
	return nil
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
