package player

import (
	"fmt"
	"sync"
	"time"
)

// Stub is a development-time Player that logs commands instead of driving
// mpv. Used when developing on non-Pi machines without a TV attached.
type Stub struct {
	mu       sync.Mutex
	current  string
	paused   bool
	startedAt time.Time
	startPos  time.Duration
}

func NewStub() *Stub { return &Stub{} }

func (s *Stub) Play(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = path
	s.paused = false
	s.startedAt = time.Now()
	s.startPos = 0
	fmt.Printf("[player:stub] play %s\n", path)
	return nil
}

func (s *Stub) PlayAt(path string, start time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = path
	s.paused = false
	s.startedAt = time.Now()
	s.startPos = start
	fmt.Printf("[player:stub] play %s (resume @ %s)\n", path, start)
	return nil
}

func (s *Stub) Position() (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == "" {
		return 0, nil
	}
	if s.paused {
		return s.startPos, nil
	}
	return s.startPos + time.Since(s.startedAt), nil
}

// Duration returns a synthetic 22-minute episode length so dev/test code
// has a sensible value to render against. Real Pi reads this from mpv.
func (s *Stub) Duration() (time.Duration, error) {
	return 22 * time.Minute, nil
}

// Seek updates the stub's playhead so subsequent Position() reads reflect
// the new offset. Used by the dev /api/seek endpoint.
func (s *Stub) Seek(pos time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pos < 0 {
		pos = 0
	}
	s.startPos = pos
	s.startedAt = time.Now()
	fmt.Printf("[player:stub] seek → %s\n", pos)
	return nil
}

func (s *Stub) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
	fmt.Printf("[player:stub] pause\n")
	return nil
}

func (s *Stub) Resume() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = false
	fmt.Printf("[player:stub] resume\n")
	return nil
}

func (s *Stub) Skip() error {
	fmt.Printf("[player:stub] skip\n")
	return nil
}

func (s *Stub) Volume(pct int) error {
	fmt.Printf("[player:stub] volume=%d\n", pct)
	return nil
}

func (s *Stub) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{NowPlaying: s.current, Paused: s.paused, Position: 0 * time.Second}
}

func (s *Stub) PlaySlideshow(paths []string, secsPer int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(paths) == 0 {
		return nil
	}
	s.current = paths[0]
	s.paused = false
	s.startedAt = time.Now()
	s.startPos = 0
	fmt.Printf("[player:stub] slideshow %d images, %ds each\n", len(paths), secsPer)
	return nil
}

func (s *Stub) Close() error { return nil }

// OnEndFile is a no-op on the stub. Tests that need to simulate
// end-of-track can call it directly to invoke the registered callback.
func (s *Stub) OnEndFile(_ func()) {}
