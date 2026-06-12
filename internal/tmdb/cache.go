package tmdb

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// Cache is a read-only view of pre-fetched TMDB metadata. The runtime
// always reads from the embedded FS — populated at build time by the
// --prefetch-tmdb CLI. Empty cache (no prefetch yet) is a valid state;
// the browse UI degrades to "no metadata loaded" instead of erroring.
//
// Layout on disk:
//   data/tmdb/show.json
//   data/tmdb/seasons/{NN}/season.json
//   data/tmdb/seasons/{NN}/episodes/{NN}.jpg     (still image, optional)
type Cache struct {
	fs    fs.FS
	root  string
}

// NewCache wraps an fs.FS that contains the embedded `data/` subtree.
// Typical use: `tmdb.NewCache(tmdb.EmbeddedFS)` — see embed.go.
func NewCache(fsys fs.FS) *Cache {
	return &Cache{fs: fsys, root: "data"}
}

// HasData returns true iff at least show.json is present. Callers use this
// to render an "empty" state instead of falling through to per-call errors.
func (c *Cache) HasData() bool {
	if c == nil || c.fs == nil {
		return false
	}
	_, err := fs.Stat(c.fs, "data/show.json")
	return err == nil
}

// Show returns the top-level show metadata or (nil, error) if missing.
func (c *Cache) Show() (*Show, error) {
	var s Show
	if err := c.readJSON("data/show.json", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Seasons returns the available season numbers in ascending order. Walks
// the embedded directory; missing seasons are skipped silently.
func (c *Cache) Seasons() ([]int, error) {
	if c.fs == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(c.fs, "data/seasons")
	if err != nil {
		return nil, nil // empty cache is fine
	}
	var nums []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(e.Name(), "%d", &n); err == nil && n > 0 {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	return nums, nil
}

// Season returns metadata for one season, including its episode list.
func (c *Cache) Season(num int) (*Season, error) {
	var s Season
	path := fmt.Sprintf("data/seasons/%02d/season.json", num)
	if err := c.readJSON(path, &s); err != nil {
		return nil, err
	}
	// Sort episodes by episode_number so the UI doesn't need to.
	sort.Slice(s.Episodes, func(i, j int) bool {
		return s.Episodes[i].EpisodeNumber < s.Episodes[j].EpisodeNumber
	})
	return &s, nil
}

// Episode returns one episode by season + episode number, or (nil, err)
// if missing. Convenience wrapper around Season() lookup.
func (c *Cache) Episode(season, episode int) (*Episode, error) {
	s, err := c.Season(season)
	if err != nil {
		return nil, err
	}
	for i := range s.Episodes {
		if s.Episodes[i].EpisodeNumber == episode {
			return &s.Episodes[i], nil
		}
	}
	return nil, fmt.Errorf("S%02dE%02d not found", season, episode)
}

// StillURL returns the URL to the cached still image for an episode, or
// empty string if no still is cached. Served by the dedicated /tmdb/ HTTP
// handler in jerry.NewServer.
func (c *Cache) StillURL(season, episode int) string {
	path := fmt.Sprintf("data/seasons/%02d/episodes/%02d.jpg", season, episode)
	if _, err := fs.Stat(c.fs, path); err != nil {
		return ""
	}
	return fmt.Sprintf("/tmdb/seasons/%02d/episodes/%02d.jpg", season, episode)
}

// FS returns the embedded subtree rooted at the cache root, so HTTP can
// mount the still images at /tmdb/* directly.
func (c *Cache) FS() fs.FS {
	if c == nil || c.fs == nil {
		return nil
	}
	sub, err := fs.Sub(c.fs, "data")
	if err != nil {
		return nil
	}
	return sub
}

func (c *Cache) readJSON(path string, out interface{}) error {
	data, err := fs.ReadFile(c.fs, path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// SuggestFileMatch returns the most likely (season, episode) for a media
// filename. Recognizes `sNNeNN_*` and similar. Used to map the queue's
// .mp4 paths to TMDB metadata for the Now Playing card.
func SuggestFileMatch(filename string) (season, episode int, ok bool) {
	name := strings.ToLower(filename)
	// strip extension
	if dot := strings.LastIndex(name, "."); dot > 0 {
		name = name[:dot]
	}
	// look for sNNeNN
	for i := 0; i+5 < len(name); i++ {
		if name[i] == 's' {
			var s, e int
			n, _ := fmt.Sscanf(name[i:], "s%de%d", &s, &e)
			if n == 2 && s > 0 && e > 0 {
				return s, e, true
			}
		}
	}
	return 0, 0, false
}

// EmbeddedFS lives in embed.go — declared at package scope via go:embed
// so main.go doesn't need to wire it.
