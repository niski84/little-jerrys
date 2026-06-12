// Package webhook fires outbound HTTP POSTs to user-configured URLs whenever
// playback events occur. Inbound webhooks reuse the existing /api/* surface;
// this package handles only the outbound side.
//
// Events fire and forget — failures are logged, never block playback. A
// missing/slow home-automation endpoint must never freeze the TV.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event is what we POST to each configured outbound URL. Receivers can use
// a single shape for everything that happens — the optional fields
// populate when relevant (Season/Episode/Title/Synopsis show up on
// episode_start, leave Path-only on simple control events like pause).
type Event struct {
	Type      string    `json:"type"` // play, pause, skip, button_press, episode_start, commercial_start, episode_resume, mode_change_*
	Path      string    `json:"path,omitempty"`
	Timestamp time.Time `json:"timestamp"`

	// Episode metadata — populated for episode_start / episode_resume
	// when the filename parses to a TMDB-known reference.
	Season   int    `json:"season,omitempty"`
	Episode  int    `json:"episode,omitempty"`
	Title    string `json:"title,omitempty"`
	Synopsis string `json:"synopsis,omitempty"`

	// Free-form context for receivers that want it (e.g. host, version).
	// Populated by the caller — empty by default.
	Source   string `json:"source,omitempty"`
}

// Sender pushes events to all configured URLs.
type Sender struct {
	mu       sync.RWMutex
	urls     []string
	client   *http.Client
}

func NewSender() *Sender {
	return &Sender{
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

// SetURLs replaces the outbound URL list.
func (s *Sender) SetURLs(urls []string) {
	clean := urls[:0]
	for _, u := range urls {
		if u != "" {
			clean = append(clean, u)
		}
	}
	s.mu.Lock()
	s.urls = clean
	s.mu.Unlock()
}

// Fire dispatches the event to all configured URLs in parallel goroutines.
// Returns immediately — POSTs run async with logging-only error handling.
func (s *Sender) Fire(evt Event) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	s.mu.RLock()
	urls := append([]string(nil), s.urls...)
	s.mu.RUnlock()
	if len(urls) == 0 {
		return
	}
	body, err := json.Marshal(evt)
	if err != nil {
		fmt.Printf("[webhook] marshal: %v\n", err)
		return
	}
	for _, u := range urls {
		go s.post(u, body)
	}
}

func (s *Sender) post(url string, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Printf("[webhook] new request %s: %v\n", url, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "little-jerrys/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		fmt.Printf("[webhook] post %s: %v\n", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Printf("[webhook] post %s: status %d\n", url, resp.StatusCode)
	}
}
