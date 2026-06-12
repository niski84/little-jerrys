// Package playlist is the true-random episode engine. It scans a media root
// for video files, applies weights and a "No Soup For You" blacklist, and
// produces a randomized queue of items (episodes interleaved with commercial
// breaks). The queue is fully *declarative* — Peek() returns exactly what
// will play, Regenerate() rebuilds it, RemoveAt/ReorderAt mutate it in
// place. The UI shows the queue and lets staff hand-edit it.
//
// True-random property: every episode plays before any repeat. When the
// queue empties, a fresh shuffle is woven and resumed.
package playlist

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ItemKind labels what a queue entry represents.
type ItemKind int

const (
	KindEpisode ItemKind = iota
	KindCommercial
)

func (k ItemKind) String() string {
	if k == KindCommercial {
		return "commercial"
	}
	return "episode"
}

// Item is one entry in the queue.
type Item struct {
	Kind    ItemKind
	Path    string
	EstSecs float64 // duration estimate in seconds (from ffprobe cache)
}

// Engine produces the next item to play.
type Engine struct {
	mu        sync.Mutex
	mediaRoot string
	rng       *rand.Rand

	queue []Item
	all   []string // all episode files, unfiltered

	blacklist map[string]bool
	weights   map[string]int

	commercials   []string
	commWeights   map[string]int
	commBlacklist map[string]bool
	commEnabled   bool

	breakAfterMins float64
	commsPerBreak  int

	durations map[string]float64 // path → minutes (from ffprobe)

	playLog []playRecord // 24h sliding window for loop guard
}

type playRecord struct {
	Path string
	When time.Time
}

// VideoExtensions are the file extensions scanned from the media root.
var VideoExtensions = []string{".mp4", ".mkv", ".mov", ".avi", ".m4v", ".webm"}

// DefaultEpisodeMinutes is the fallback when ffprobe is unavailable or fails.
const DefaultEpisodeMinutes = 22.0

// New constructs an Engine rooted at mediaRoot (typically the USB mount).
func New(mediaRoot string) *Engine {
	return &Engine{
		mediaRoot:      mediaRoot,
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
		blacklist:      map[string]bool{},
		weights:        map[string]int{},
		commWeights:    map[string]int{},
		commBlacklist:  map[string]bool{},
		durations:      map[string]float64{},
		breakAfterMins: 22,
		commsPerBreak:  2,
	}
}

// Scan walks the media root and refreshes the catalog. Episodes live at the
// root; commercials live under root/commercials/. Durations are probed via
// ffprobe (best-effort, falls back to DefaultEpisodeMinutes).
func (e *Engine) Scan() error {
	e.mu.Lock()
	e.all = e.all[:0]
	e.commercials = e.commercials[:0]
	root := e.mediaRoot
	e.mu.Unlock()

	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("media root not accessible: %s", root)
	}

	var eps, ads []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		if !isVideo(path) {
			return nil
		}
		if strings.Contains(path, string(os.PathSeparator)+"commercials"+string(os.PathSeparator)) {
			ads = append(ads, path)
			return nil
		}
		eps = append(eps, path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk media root: %w", err)
	}

	e.mu.Lock()
	e.all = eps
	e.commercials = ads
	e.queue = nil // force regenerate on next Next()
	e.mu.Unlock()

	go e.probeDurations(append(append([]string{}, eps...), ads...))
	return nil
}

func (e *Engine) probeDurations(paths []string) {
	for _, p := range paths {
		if _, ok := e.duration(p); ok {
			continue
		}
		mins := probeMinutes(p)
		e.mu.Lock()
		e.durations[p] = mins
		e.mu.Unlock()
	}
}

func (e *Engine) duration(path string) (float64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.durations[path]
	return v, ok
}

// probeMinutes returns video duration in minutes via ffprobe.
func probeMinutes(path string) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path,
	).Output()
	if err != nil {
		return DefaultEpisodeMinutes
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || secs <= 0 {
		return DefaultEpisodeMinutes
	}
	return secs / 60.0
}

func isVideo(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, v := range VideoExtensions {
		if ext == v {
			return true
		}
	}
	return false
}

// SetBlacklist replaces the current episode blacklist. Keys are SNNENN
// references (e.g. "S02E11") — same convention as SetEpisodeWeight, so a
// single user-facing identity ("episode") drives both controls.
//
// Does NOT invalidate the current queue — a setting change shouldn't
// silently wipe a user's curated playlist mid-play. The new value applies
// to the next Regenerate (or to the next pool reshuffle when the queue
// naturally exhausts). The dashboard surfaces a "Regenerate" button that
// makes this explicit.
func (e *Engine) SetBlacklist(seasonEpisodeKeys []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.blacklist = make(map[string]bool, len(seasonEpisodeKeys))
	for _, k := range seasonEpisodeKeys {
		e.blacklist[k] = true
	}
}

// SetEpisodeWeight sets a per-episode play multiplier keyed by TMDB
// reference ("S02E11"). The engine resolves catalog filenames to keys via
// SuggestFileMatch on the fly when generating the weighted pool, so weights
// stick across USB swaps as long as the new files keep the same `sNNeNN_*`
// naming.
func (e *Engine) SetEpisodeWeight(seasonEpisodeKey string, weight int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if weight <= 1 {
		delete(e.weights, seasonEpisodeKey)
	} else {
		e.weights[seasonEpisodeKey] = weight
	}
	// Queue is NOT invalidated — applies on next Regenerate.
}

// SetCommercialWeight biases a single commercial in the random pool.
func (e *Engine) SetCommercialWeight(path string, weight int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if weight <= 1 {
		delete(e.commWeights, path)
	} else {
		e.commWeights[path] = weight
	}
}

// SetCommercialBlacklist removes specific commercials from rotation.
func (e *Engine) SetCommercialBlacklist(paths []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.commBlacklist = make(map[string]bool, len(paths))
	for _, p := range paths {
		e.commBlacklist[p] = true
	}
}

// EnableCommercials toggles the commercial pack.
func (e *Engine) EnableCommercials(on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.commEnabled = on
}

// SetBreakSchedule configures broadcast-style break frequency.
func (e *Engine) SetBreakSchedule(afterMins, perBreak int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if afterMins < 1 {
		afterMins = 22
	}
	if perBreak < 1 {
		perBreak = 1
	}
	if perBreak > 10 {
		perBreak = 10
	}
	e.breakAfterMins = float64(afterMins)
	e.commsPerBreak = perBreak
}

// Next returns the next item to play and advances the queue. Triggers a
// regeneration if the queue is empty.
func (e *Engine) Next() (Item, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.queue) == 0 {
		e.regenerateLocked()
	}
	if len(e.queue) == 0 {
		return Item{}, fmt.Errorf("no playable files in %s", e.mediaRoot)
	}
	head := e.queue[0]
	e.queue = e.queue[1:]
	if head.Kind == KindEpisode {
		e.recordPlay(head.Path)
	}
	return head, nil
}

// Peek returns up to n upcoming items without consuming the queue. n<=0
// returns the entire current queue.
func (e *Engine) Peek(n int) []Item {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.queue) == 0 {
		return nil
	}
	if n <= 0 || n > len(e.queue) {
		n = len(e.queue)
	}
	out := make([]Item, n)
	copy(out, e.queue[:n])
	return out
}

// Regenerate rebuilds the queue from scratch using current weights, blacklist,
// and commercial schedule. Call this after the user edits weights to surface
// the change immediately.
func (e *Engine) Regenerate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.regenerateLocked()
}

// LoadQueue replaces the current queue with the given items in order. Used
// by the "Play this playlist" action — once consumed, regenerateLocked()
// fires a fresh random shuffle so the rotation continues.
func (e *Engine) LoadQueue(items []Item) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queue = append(e.queue[:0], items...)
}

// ResolvePathForEpisode walks the episode catalog for a file matching
// `sNNeNN_*` (case-insensitive). Returns "" if no match exists.
func (e *Engine) ResolvePathForEpisode(season, episode int) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	want := fmt.Sprintf("s%02de%02d", season, episode)
	for _, p := range e.all {
		base := strings.ToLower(filepath.Base(p))
		if strings.HasPrefix(base, want) || strings.Contains(base, "_"+want) {
			return p
		}
	}
	return ""
}

// RemoveAt drops the item at index i. Used by the "Skip" button next to
// individual queue entries.
func (e *Engine) RemoveAt(i int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.queue) {
		return fmt.Errorf("index %d out of range [0,%d)", i, len(e.queue))
	}
	e.queue = append(e.queue[:i], e.queue[i+1:]...)
	return nil
}

// ReorderAt moves the item at fromIdx to toIdx. Used by drag-and-drop.
func (e *Engine) ReorderAt(fromIdx, toIdx int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if fromIdx < 0 || fromIdx >= len(e.queue) {
		return fmt.Errorf("from %d out of range", fromIdx)
	}
	if toIdx < 0 || toIdx >= len(e.queue) {
		return fmt.Errorf("to %d out of range", toIdx)
	}
	if fromIdx == toIdx {
		return nil
	}
	item := e.queue[fromIdx]
	e.queue = append(e.queue[:fromIdx], e.queue[fromIdx+1:]...)
	if toIdx > fromIdx {
		toIdx--
	}
	e.queue = append(e.queue[:toIdx], append([]Item{item}, e.queue[toIdx:]...)...)
	return nil
}

// regenerateLocked rebuilds the queue. Caller must hold e.mu.
func (e *Engine) regenerateLocked() {
	episodes := e.weightedEpisodePool()
	e.rng.Shuffle(len(episodes), func(i, j int) { episodes[i], episodes[j] = episodes[j], episodes[i] })

	q := make([]Item, 0, len(episodes)*2)
	var elapsedMins float64
	for _, ep := range episodes {
		dur := e.durations[ep]
		if dur == 0 {
			dur = DefaultEpisodeMinutes
		}
		q = append(q, Item{Kind: KindEpisode, Path: ep, EstSecs: dur * 60})
		elapsedMins += dur

		// Time for a commercial break?
		if e.commEnabled && len(e.commercials) > 0 && elapsedMins >= e.breakAfterMins {
			for i := 0; i < e.commsPerBreak; i++ {
				ad := e.pickCommercialLocked()
				if ad == "" {
					break
				}
				adDur := e.durations[ad]
				if adDur == 0 {
					adDur = 0.5 // 30s default
				}
				q = append(q, Item{Kind: KindCommercial, Path: ad, EstSecs: adDur * 60})
			}
			elapsedMins = 0
		}
	}
	e.queue = q
}

func (e *Engine) weightedEpisodePool() []string {
	pool := make([]string, 0, len(e.all))
	for _, p := range e.all {
		// Both blacklist and weight live in SNNENN-keyed maps (parsed
		// from filename). Files without a parseable name pass through
		// with weight 1 and never get blocked.
		key := episodeKeyFromPath(p)
		if key != "" && e.blacklist[key] {
			continue
		}
		mult := 1
		if key != "" {
			if w, ok := e.weights[key]; ok && w > 1 {
				mult = w
			}
		}
		for i := 0; i < mult; i++ {
			pool = append(pool, p)
		}
	}
	return pool
}

// episodeKeyFromPath returns "S02E11" for a filename like "s02e11_chinese.mp4".
// Returns "" if the filename doesn't carry a parseable sNNeNN.
func episodeKeyFromPath(path string) string {
	name := strings.ToLower(filepath.Base(path))
	if dot := strings.LastIndex(name, "."); dot > 0 {
		name = name[:dot]
	}
	for i := 0; i+5 < len(name); i++ {
		if name[i] != 's' {
			continue
		}
		var s, ep int
		n, _ := fmt.Sscanf(name[i:], "s%de%d", &s, &ep)
		if n == 2 && s > 0 && ep > 0 {
			return fmt.Sprintf("S%02dE%02d", s, ep)
		}
	}
	return ""
}

func (e *Engine) pickCommercialLocked() string {
	pool := make([]string, 0, len(e.commercials))
	for _, c := range e.commercials {
		if e.commBlacklist[c] {
			continue
		}
		mult := 1
		if w, ok := e.commWeights[c]; ok && w > 1 {
			mult = w
		}
		for i := 0; i < mult; i++ {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		return ""
	}
	return pool[e.rng.Intn(len(pool))]
}

func (e *Engine) recordPlay(path string) {
	now := time.Now()
	cutoff := now.Add(-24 * time.Hour)
	pruned := e.playLog[:0]
	for _, r := range e.playLog {
		if r.When.After(cutoff) {
			pruned = append(pruned, r)
		}
	}
	e.playLog = append(pruned, playRecord{Path: path, When: now})
}

// LoopGuard returns paths that have played more than threshold times in 24h.
func (e *Engine) LoopGuard(threshold int) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	counts := map[string]int{}
	for _, r := range e.playLog {
		counts[r.Path]++
	}
	var hot []string
	for p, c := range counts {
		if c > threshold {
			hot = append(hot, p)
		}
	}
	return hot
}

// Stats returns catalog counts for the dashboard.
type Stats struct {
	TotalEpisodes       int
	BlacklistedCount    int
	WeightedCount       int
	CommercialsLoaded   int
	CommercialsEnabled  bool
	BreakAfterMinutes   int
	CommercialsPerBreak int
	QueueRemaining      int
}

func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Stats{
		TotalEpisodes:       len(e.all),
		BlacklistedCount:    len(e.blacklist),
		WeightedCount:       len(e.weights),
		CommercialsLoaded:   len(e.commercials),
		CommercialsEnabled:  e.commEnabled,
		BreakAfterMinutes:   int(e.breakAfterMins),
		CommercialsPerBreak: e.commsPerBreak,
		QueueRemaining:      len(e.queue),
	}
}

// Catalog returns the full unfiltered episode list (for blacklist UI).
func (e *Engine) Catalog() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.all))
	copy(out, e.all)
	return out
}

// CommercialCatalog returns the full commercial list (for the commercials UI).
func (e *Engine) CommercialCatalog() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.commercials))
	copy(out, e.commercials)
	return out
}
