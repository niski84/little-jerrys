// Package gpio listens for the bailout button — a physical arcade button
// wired to a Pi GPIO pin. On a falling edge (button press), the registered
// handler fires (typically: skip current episode).
//
// On non-Pi builds (e.g. dev laptop), Listen returns a stub that does
// nothing. Real Pi support is gated behind the `pi` build tag and uses
// periph.io.
package gpio

import (
	"context"
	"sync"
	"time"
)

// Listener watches a GPIO pin for falling-edge events.
type Listener interface {
	Start(ctx context.Context, onPress func()) error
	Close() error
}

// Config configures the bailout button.
type Config struct {
	Pin       int           // BCM pin number (default 17)
	Debounce  time.Duration // ignore repeats inside this window
	PullUp    bool          // pin held high, button presses pull to ground
	Simulated bool          // force the stub even on Pi (for tests)
}

// New returns a Listener appropriate for the build environment. On non-Pi
// builds, this is always a stub.
func New(cfg Config) Listener {
	if cfg.Debounce == 0 {
		cfg.Debounce = 250 * time.Millisecond
	}
	if cfg.Simulated {
		return &stubListener{}
	}
	return newPlatformListener(cfg)
}

// stubListener is the default on non-Pi platforms. The Simulate method lets
// tests or the admin UI fake button presses.
type stubListener struct {
	mu       sync.Mutex
	onPress  func()
	debounce time.Duration
	last     time.Time
}

func (s *stubListener) Start(ctx context.Context, onPress func()) error {
	s.mu.Lock()
	s.onPress = onPress
	s.debounce = 250 * time.Millisecond
	s.mu.Unlock()
	return nil
}

func (s *stubListener) Close() error { return nil }

// Simulate fires the press handler — used by /api/button/press for testing.
func (s *stubListener) Simulate() {
	s.mu.Lock()
	if time.Since(s.last) < s.debounce {
		s.mu.Unlock()
		return
	}
	s.last = time.Now()
	cb := s.onPress
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}
