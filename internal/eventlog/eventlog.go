// Package eventlog is the in-process ring buffer of human-readable events
// the appliance has emitted. The dashboard's bottom strip renders the
// latest few; a /api/events/recent endpoint serves the full ring as JSON
// so an operator can copy-paste the last N events into a support email.
//
// Design choices:
//   - In-memory only — events from before the last reboot don't survive.
//     The appliance's `*.log` file on disk has the full historical record
//     for forensic stuff; this is the "what just happened" surface.
//   - Capped at a fixed capacity (oldest entries drop on overflow).
//   - Push() also writes to stdout so the existing log file stays useful.
//   - Newest-first when read; ring is internally newest-last for cheap
//     append.
package eventlog

import (
	"fmt"
	"sync"
	"time"
)

// Event is one human-readable entry.
type Event struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`   // info | warn | error
	Source  string    `json:"source"`  // playback, auth, network, gpio, schedule, ...
	Message string    `json:"message"`
}

// Log is the ring buffer.
type Log struct {
	mu       sync.Mutex
	events   []Event
	capacity int
}

// New constructs a ring with the given capacity. 200 is a comfortable default.
func New(capacity int) *Log {
	if capacity < 16 {
		capacity = 16
	}
	return &Log{capacity: capacity}
}

// Push appends an event and trims the ring to capacity. Also echoes the
// message to stdout in the existing `[source] message` style so anyone
// watching `journalctl` / the log file sees the same thing.
func (l *Log) Push(level, source, message string) {
	if l == nil {
		return
	}
	e := Event{Time: time.Now(), Level: level, Source: source, Message: message}
	l.mu.Lock()
	l.events = append(l.events, e)
	if len(l.events) > l.capacity {
		l.events = l.events[len(l.events)-l.capacity:]
	}
	l.mu.Unlock()
	prefix := ""
	if level == "warn" {
		prefix = "! "
	} else if level == "error" {
		prefix = "✗ "
	}
	fmt.Printf("[%s] %s%s\n", source, prefix, message)
}

// Convenience wrappers.
func (l *Log) Info(source, msg string)  { l.Push("info", source, msg) }
func (l *Log) Warn(source, msg string)  { l.Push("warn", source, msg) }
func (l *Log) Error(source, msg string) { l.Push("error", source, msg) }

// Infof / Warnf / Errorf are sprintf wrappers for callers that want
// formatted strings without a separate fmt.Sprintf at the call site.
func (l *Log) Infof(source, format string, args ...interface{}) {
	l.Push("info", source, fmt.Sprintf(format, args...))
}
func (l *Log) Warnf(source, format string, args ...interface{}) {
	l.Push("warn", source, fmt.Sprintf(format, args...))
}
func (l *Log) Errorf(source, format string, args ...interface{}) {
	l.Push("error", source, fmt.Sprintf(format, args...))
}

// Recent returns up to n events, newest first.
func (l *Log) Recent(n int) []Event {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.events) {
		n = len(l.events)
	}
	out := make([]Event, n)
	// Copy the last n, newest-last; then reverse for newest-first ordering.
	copy(out, l.events[len(l.events)-n:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
