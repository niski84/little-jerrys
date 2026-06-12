package jerry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/niski84/little-jerrys/internal/schedule"
)

// State is the durable bag of admin-controlled config. Persisted as JSON on
// the USB drive so it survives Pi reboots, SD reflashes, and dev/prod swaps.
type State struct {
	Blacklist          []string       `json:"blacklist"`
	Weights            map[string]int `json:"weights"`
	CommercialsEnabled bool           `json:"commercials_enabled"`

	// Per-commercial weights and blacklist — same model as episodes.
	CommercialWeights   map[string]int `json:"commercial_weights"`
	CommercialBlacklist []string       `json:"commercial_blacklist"`

	// Broadcast-station-style scheduling.
	BreakAfterMinutes   int `json:"break_after_minutes"`   // insert break every N minutes of episode time
	CommercialsPerBreak int `json:"commercials_per_break"` // ads back-to-back per break

	AdminPasswordHash  string   `json:"admin_password_hash"`
	PasswordIsDefault  bool     `json:"password_is_default"`
	SessionKey         string   `json:"session_key"` // HMAC signing key for stateless cookies; generated once on first boot
	WebhookOutbound    []string `json:"webhook_outbound"`
	LoopGuardThreshold int      `json:"loop_guard_threshold"`

	// Resume-from-position. When the Pi powers off at night and boots back
	// up the next morning, mpv loads LastTrack at LastPositionSecs so the
	// rotation picks up mid-episode rather than restarting cold.
	LastTrack         string  `json:"last_track,omitempty"`
	LastPositionSecs  float64 `json:"last_position_secs,omitempty"`

	// PlayCounts is the *started* count — incremented every time PlayNext
	// loads a file, regardless of how long the user actually watches.
	PlayCounts map[string]int `json:"play_counts"`

	// CompletedCounts is the *real* watch count — incremented once per
	// playback when the position crosses 75% of the file's duration. The
	// number we'd put on a "what's actually getting played" report.
	CompletedCounts map[string]int `json:"completed_counts"`

	// Aggregate watch time, accumulated by the playbackTracker goroutine.
	// MinutesPlayed ticks while a file is actually playing; MinutesPaused
	// ticks while we're paused. Useful for the weekly summary dashboard.
	MinutesPlayed float64 `json:"minutes_played"`
	MinutesPaused float64 `json:"minutes_paused"`

	// Week-rollover snapshot: stores the lifetime values at the start of
	// the current Mon-Sun week. "This week" stats = current values minus
	// snapshot. When a new week begins (current Monday > WeekStart), the
	// previous week's delta is rolled into LastWeekRollup and the
	// snapshot is reset.
	WeekStart                string         `json:"week_start"` // ISO date (Mon)
	WeekStartPlayCounts      map[string]int `json:"week_start_play_counts"`
	WeekStartCompletedCounts map[string]int `json:"week_start_completed_counts"`
	WeekStartMinutesPlayed   float64        `json:"week_start_minutes_played"`
	WeekStartMinutesPaused   float64        `json:"week_start_minutes_paused"`
	LastWeekRollup           *WeekRollup    `json:"last_week_rollup,omitempty"`

	// Hours of operation. Outside any active window, the appliance switches
	// to the fan-art slideshow. If empty, the box is "always on" (default).
	Schedules []schedule.Schedule `json:"schedules"`

	// Fan-art slideshow (off-hours mode).
	FanArtEnabled      bool `json:"fan_art_enabled"`
	FanArtSecsPerImage int  `json:"fan_art_secs_per_image"`

	// Secondary HDMI slideshow — runs independently on the Pi's second HDMI
	// output via chromium kiosk. Layout is the number of images shown at
	// once: 1, 4 (2x2), or 16 (4x4).
	SlideshowEnabled bool `json:"slideshow_enabled"`
	SlideshowLayout  int  `json:"slideshow_layout"`
	SlideshowSecsPer int  `json:"slideshow_secs_per"`

	// User-curated playlists. Each is an ordered list of episode refs.
	// "Play this playlist" replaces the random rotation queue with this
	// list; once consumed, normal random rotation resumes.
	Playlists []Playlist `json:"playlists"`

	// Live playback queue snapshot — what's coming up after the currently
	// playing track. Persisted so a reboot doesn't lose the lineup; the
	// dashboard's "Up next" section repopulates immediately on boot.
	QueueItems []QueueSnapshotItem `json:"queue_items"`
}

// WeekRollup captures the playback summary for a completed week. Frozen
// at week-rollover time so it isn't recomputed on every render.
type WeekRollup struct {
	Start             string         `json:"start"` // ISO Mon
	EpisodesStarted   int            `json:"episodes_started"`
	EpisodesCompleted int            `json:"episodes_completed"`
	UniqueEpisodes    int            `json:"unique_episodes"`
	MinutesPlayed     float64        `json:"minutes_played"`
	MinutesPaused     float64        `json:"minutes_paused"`
	PerEpisode        map[string]int `json:"per_episode"` // path → completed count this week
}

// QueueSnapshotItem mirrors playlist.Item in JSON form so we can persist
// the engine's queue without leaking the playlist package's types into
// the state file.
type QueueSnapshotItem struct {
	Kind    string  `json:"kind"`     // "episode" | "commercial"
	Path    string  `json:"path"`
	EstSecs float64 `json:"est_secs"`
}

// Playlist is a named, ordered collection of episode references.
type Playlist struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Episodes []EpisodeRef `json:"episodes"`
	Created  string       `json:"created"` // ISO date
}

// EpisodeRef points at a TMDB-cataloged episode by season + episode number.
// File-system path is resolved at play time via the catalog.
type EpisodeRef struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

// DefaultState seeds a fresh install — admin/admin with the change-required
// flag set so the playback controls stay locked until first login.
func DefaultState() State {
	return State{
		Blacklist:           []string{},
		Weights:             map[string]int{},
		CommercialsEnabled:  false,
		CommercialWeights:   map[string]int{},
		CommercialBlacklist: []string{},
		BreakAfterMinutes:   22, // one break per ~22-min sitcom episode
		CommercialsPerBreak: 2,  // 90s broadcast feel: 2 ads back-to-back
		PlayCounts:          map[string]int{},
		Schedules:           []schedule.Schedule{},
		FanArtEnabled:       true,
		FanArtSecsPerImage:  8,
		SlideshowEnabled:    false,
		SlideshowLayout:     4, // 2x2 default
		SlideshowSecsPer:    12,
		// bcrypt("admin") — generated at runtime in NewApp on first boot.
		PasswordIsDefault:  true,
		LoopGuardThreshold: 3,
	}
}

// Store persists State to disk under a single mutex.
type Store struct {
	mu   sync.RWMutex
	path string
	cur  State
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path, cur: DefaultState()}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load state: %w", err)
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	if st.Weights == nil {
		st.Weights = map[string]int{}
	}
	if st.CommercialWeights == nil {
		st.CommercialWeights = map[string]int{}
	}
	if st.BreakAfterMinutes == 0 {
		st.BreakAfterMinutes = 22
	}
	if st.CommercialsPerBreak == 0 {
		st.CommercialsPerBreak = 2
	}
	if st.FanArtSecsPerImage == 0 {
		st.FanArtSecsPerImage = 8
	}
	if st.PlayCounts == nil {
		st.PlayCounts = map[string]int{}
	}
	if st.CompletedCounts == nil {
		st.CompletedCounts = map[string]int{}
	}
	if st.WeekStartPlayCounts == nil {
		st.WeekStartPlayCounts = map[string]int{}
	}
	if st.WeekStartCompletedCounts == nil {
		st.WeekStartCompletedCounts = map[string]int{}
	}
	// Migrate legacy path-keyed weights/blacklist (pre-S/E refactor) by
	// dropping anything that doesn't look like a SNNENN reference.
	st.Blacklist = onlyEpisodeKeys(st.Blacklist)
	if st.Weights != nil {
		clean := map[string]int{}
		for k, v := range st.Weights {
			if isEpisodeKey(k) {
				clean[k] = v
			}
		}
		st.Weights = clean
	}
	if st.SlideshowLayout == 0 {
		st.SlideshowLayout = 4
	}
	if st.SlideshowSecsPer == 0 {
		st.SlideshowSecsPer = 12
	}
	s.cur = st
	return nil
}

// Save writes the current state to disk atomically.
func (s *Store) Save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.cur, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}
	return nil
}

// Get returns a copy of the current state.
func (s *Store) Get() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := s.cur
	cp.Blacklist = append([]string(nil), s.cur.Blacklist...)
	cp.Weights = make(map[string]int, len(s.cur.Weights))
	for k, v := range s.cur.Weights {
		cp.Weights[k] = v
	}
	cp.WebhookOutbound = append([]string(nil), s.cur.WebhookOutbound...)
	cp.CommercialBlacklist = append([]string(nil), s.cur.CommercialBlacklist...)
	cp.CommercialWeights = make(map[string]int, len(s.cur.CommercialWeights))
	for k, v := range s.cur.CommercialWeights {
		cp.CommercialWeights[k] = v
	}
	cp.PlayCounts = make(map[string]int, len(s.cur.PlayCounts))
	for k, v := range s.cur.PlayCounts {
		cp.PlayCounts[k] = v
	}
	cp.CompletedCounts = make(map[string]int, len(s.cur.CompletedCounts))
	for k, v := range s.cur.CompletedCounts {
		cp.CompletedCounts[k] = v
	}
	cp.WeekStartPlayCounts = make(map[string]int, len(s.cur.WeekStartPlayCounts))
	for k, v := range s.cur.WeekStartPlayCounts {
		cp.WeekStartPlayCounts[k] = v
	}
	cp.WeekStartCompletedCounts = make(map[string]int, len(s.cur.WeekStartCompletedCounts))
	for k, v := range s.cur.WeekStartCompletedCounts {
		cp.WeekStartCompletedCounts[k] = v
	}
	if s.cur.LastWeekRollup != nil {
		rb := *s.cur.LastWeekRollup
		rb.PerEpisode = make(map[string]int, len(s.cur.LastWeekRollup.PerEpisode))
		for k, v := range s.cur.LastWeekRollup.PerEpisode {
			rb.PerEpisode[k] = v
		}
		cp.LastWeekRollup = &rb
	}
	cp.Playlists = make([]Playlist, len(s.cur.Playlists))
	for i, p := range s.cur.Playlists {
		eps := make([]EpisodeRef, len(p.Episodes))
		copy(eps, p.Episodes)
		cp.Playlists[i] = Playlist{ID: p.ID, Name: p.Name, Episodes: eps, Created: p.Created}
	}
	cp.QueueItems = append([]QueueSnapshotItem(nil), s.cur.QueueItems...)
	return cp
}

// isEpisodeKey returns true for "SNNENN" — the user-visible reference for
// a TMDB episode. Used to filter legacy path-keyed entries during state
// migration.
func isEpisodeKey(s string) bool {
	if len(s) != 6 || s[0] != 'S' || s[3] != 'E' {
		return false
	}
	for _, i := range []int{1, 2, 4, 5} {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// onlyEpisodeKeys returns the input with any non-SNNENN entries dropped.
func onlyEpisodeKeys(in []string) []string {
	out := in[:0]
	for _, k := range in {
		if isEpisodeKey(k) {
			out = append(out, k)
		}
	}
	return out
}

// Update applies a mutator under the write lock and persists.
func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	fn(&s.cur)
	s.mu.Unlock()
	return s.Save()
}
