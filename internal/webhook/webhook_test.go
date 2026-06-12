package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFireDispatchesToAllURLs(t *testing.T) {
	var hits int32
	var mu sync.Mutex
	var bodies []map[string]any

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body, _ := io.ReadAll(r.Body)
		var evt map[string]any
		_ = json.Unmarshal(body, &evt)
		mu.Lock()
		bodies = append(bodies, evt)
		mu.Unlock()
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv2.Close()

	s := NewSender()
	s.SetURLs([]string{srv1.URL, srv2.URL})
	s.Fire(Event{Type: "skip", Path: "/some/episode.mp4"})

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&hits) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("want 2 webhook hits, got %d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) > 0 && bodies[0]["type"] != "skip" {
		t.Errorf("event body type = %v, want skip", bodies[0]["type"])
	}
}

func TestFireWithNoURLsIsNoop(t *testing.T) {
	s := NewSender()
	// Should not panic, should not block.
	done := make(chan struct{})
	go func() {
		s.Fire(Event{Type: "play"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Fire blocked with no URLs configured")
	}
}

func TestSlowEndpointDoesNotBlockCaller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // exceed sender timeout
	}))
	defer srv.Close()

	s := NewSender()
	s.SetURLs([]string{srv.URL})
	start := time.Now()
	s.Fire(Event{Type: "skip"})
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("Fire blocked for %v on slow endpoint — should be async", elapsed)
	}
}
