package player

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

const (
	progressPollInterval = 5 * time.Second
	progressStallAfter   = 30 * time.Second
	progressBufferGrace  = 120 * time.Second
	progressBudgetWindow = 10 * time.Minute
	progressMaxAttempts  = 3
)

// PlaybackHealth reports clock progress and bounded automatic recovery without
// exposing the playing path. Applications may discover this method by type
// assertion; it is intentionally not part of Player.
type PlaybackHealth struct {
	State           string        `json:"state"`
	LastProgressAge time.Duration `json:"last_progress_age"`
	RecoveryCount   int           `json:"recovery_count"`
	Reason          string        `json:"reason,omitempty"`
}

type progressSample struct {
	position                    time.Duration
	paused, eof, idle, coreIdle bool
	buffering, seeking          bool
	static, slideshow           bool
}

type progressTracker struct {
	identity          uint64
	position          time.Duration
	lastProgress      time.Time
	bufferSince       time.Time
	initialized       bool
	awaitingProgress  bool
	queryFailureSince time.Time
	failed            bool
	failedIdentity    uint64
	stallAfter        time.Duration
	bufferGrace       time.Duration
}

// observe returns eligibility, current state, and whether progress is stalled.
func (t *progressTracker) observe(now time.Time, identity uint64, s progressSample) (bool, string, bool, bool) {
	if identity != t.identity || !t.initialized {
		t.identity, t.position, t.lastProgress = identity, s.position, now
		t.initialized = true
		t.awaitingProgress = true
		t.bufferSince = time.Time{}
	}
	switch {
	case s.paused:
		return false, "paused", false, false
	case s.slideshow:
		return false, "slideshow", false, false
	case s.static:
		return false, "static", false, false
	case s.buffering:
		if t.bufferSince.IsZero() {
			t.bufferSince = now
		}
		if now.Sub(t.bufferSince) < t.bufferGrace {
			return false, "buffering", false, false
		}
	}
	if !s.buffering {
		t.bufferSince = time.Time{}
	}
	if s.eof {
		return false, "eof", false, false
	}
	if s.seeking {
		t.position, t.lastProgress = s.position, now
		return false, "seeking", false, false
	}
	if (s.idle || s.coreIdle) && (!s.buffering || now.Sub(t.bufferSince) < t.bufferGrace) {
		return false, "idle", false, false
	}
	if s.position > t.position+250*time.Millisecond {
		t.position, t.lastProgress = s.position, now
		t.awaitingProgress = false
		return true, "playing", false, true
	}
	if s.position < t.position-250*time.Millisecond {
		// User seek or a new timeline; allow it a fresh stall interval.
		t.position, t.lastProgress = s.position, now
		t.awaitingProgress = false
		return true, "playing", false, true
	}
	state := "playing"
	if t.awaitingProgress {
		state = "recovering"
	}
	return true, state, now.Sub(t.lastProgress) >= t.stallAfter, false
}

type progressBudget struct {
	attempts []time.Time
}

// take reserves one recovery. The first is an in-process reload; later
// attempts escalate to killing mpv for its existing supervisor to restart.
func (b *progressBudget) take(now time.Time) (string, bool) {
	keep := b.attempts[:0]
	for _, at := range b.attempts {
		if now.Sub(at) < progressBudgetWindow {
			keep = append(keep, at)
		}
	}
	b.attempts = keep
	if len(keep) >= progressMaxAttempts {
		return "", false
	}
	b.attempts = append(b.attempts, now)
	if len(b.attempts) == 1 {
		return "reload", true
	}
	return "restart", true
}

func (p *mpvPlayer) PlaybackHealth() PlaybackHealth {
	p.healthMu.RLock()
	h := p.health
	at := p.healthAt
	p.healthMu.RUnlock()
	if !at.IsZero() && (h.State == "playing" || h.State == "recovering") {
		h.LastProgressAge = time.Since(at)
	}
	return h
}

func (p *mpvPlayer) setPlaybackHealth(state, reason string, at time.Time, count int) {
	p.healthMu.Lock()
	p.health = PlaybackHealth{State: state, Reason: reason, RecoveryCount: count}
	p.healthAt = at
	p.healthMu.Unlock()
}

func (p *mpvPlayer) diagnostic(message string) {
	if p.cfg.OnDiagnostic != nil {
		p.cfg.OnDiagnostic(message)
	}
}

func (p *mpvPlayer) progressLoop(ctx context.Context) {
	ticker := time.NewTicker(progressPollInterval)
	defer ticker.Stop()
	tracker := progressTracker{stallAfter: progressStallAfter, bufferGrace: progressBufferGrace}
	var budget progressBudget
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			p.checkProgress(ctx, now, &tracker, &budget)
		}
	}
}

func (p *mpvPlayer) checkProgress(ctx context.Context, now time.Time, tracker *progressTracker, budget *progressBudget) {
	p.mu.Lock()
	path, paused, slideshow, closed, identity := p.current, p.paused, p.slideshow, p.closed, p.intent
	p.mu.Unlock()
	if tracker.failed {
		if identity == tracker.failedIdentity {
			p.setPlaybackHealth("failed", "recovery budget exhausted", now, len(budget.attempts))
			return
		}
		tracker.failed = false
		tracker.initialized = false
		tracker.awaitingProgress = true
		tracker.queryFailureSince = time.Time{}
	}
	if closed || path == "" {
		return
	}
	if paused || slideshow || isStillImage(path) {
		tracker.queryFailureSince = time.Time{}
		state := "paused"
		if slideshow {
			state = "slideshow"
		} else if !paused {
			state = "static"
		}
		p.setPlaybackHealth(state, "", now, len(budget.attempts))
		return
	}
	s, err := p.readProgressSample(ctx)
	if err != nil {
		if tracker.queryFailureSince.IsZero() {
			tracker.queryFailureSince = now
		}
		if now.Sub(tracker.queryFailureSince) < tracker.stallAfter {
			p.setPlaybackHealth("unavailable", "mpv progress query failed", now, len(budget.attempts))
			return
		}
		_, ok := budget.take(now)
		if !ok {
			tracker.failed = true
			tracker.failedIdentity = identity
			p.setPlaybackHealth("failed", "recovery budget exhausted", tracker.queryFailureSince, len(budget.attempts))
			p.diagnostic("mpv progress queries failed; recovery budget exhausted")
			return
		}
		p.mu.Lock()
		pos := p.lastPosition
		p.mu.Unlock()
		p.setPlaybackHealth("recovering", "restart: progress IPC unavailable", tracker.queryFailureSince, len(budget.attempts))
		p.diagnostic("mpv progress IPC unavailable; restarting owned process")
		if p.recoverProgress(ctx, identity, path, pos, "restart") {
			tracker.queryFailureSince = now
			tracker.initialized = false
			tracker.awaitingProgress = true
		} else {
			p.setPlaybackHealth("unavailable", "mpv restart request failed", now, len(budget.attempts))
		}
		return
	}
	tracker.queryFailureSince = time.Time{}
	s.paused = s.paused || paused
	s.slideshow = slideshow
	s.static = isStillImage(path)
	eligible, state, stalled, progressed := tracker.observe(now, identity, s)
	_ = progressed // Keep the hard rolling budget even when recovery briefly advances.
	if !eligible {
		p.setPlaybackHealth(state, "", now, len(budget.attempts))
		return
	}
	if !stalled {
		p.setPlaybackHealth(state, "", tracker.lastProgress, len(budget.attempts))
		return
	}
	action, ok := budget.take(now)
	if !ok {
		tracker.failed = true
		tracker.failedIdentity = identity
		p.setPlaybackHealth("failed", "recovery budget exhausted", tracker.lastProgress, len(budget.attempts))
		p.diagnostic("mpv playback stalled; recovery budget exhausted")
		return
	}
	reason := "no time-pos progress for 30s"
	p.setPlaybackHealth("recovering", action+": "+reason, tracker.lastProgress, len(budget.attempts))
	p.diagnostic("mpv playback stalled; attempting " + action)
	if p.recoverProgress(ctx, identity, path, s.position, action) {
		tracker.initialized = false
		tracker.awaitingProgress = true
		p.setPlaybackHealth("recovering", action+" requested", now, len(budget.attempts))
	} else {
		p.setPlaybackHealth("unavailable", action+" failed", now, len(budget.attempts))
		p.diagnostic("mpv playback recovery command failed")
	}
}

func (p *mpvPlayer) readProgressSample(ctx context.Context) (progressSample, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	props := []string{"pause", "eof-reached", "idle-active", "core-idle", "paused-for-cache", "seeking"}
	values := make([]bool, len(props))
	for i, prop := range props {
		v, err := p.queryContext(ctx, "get_property", prop)
		if err != nil {
			return progressSample{}, err
		}
		values[i], _ = v.(bool)
	}
	v, err := p.queryContext(ctx, "get_property", "time-pos")
	if err != nil {
		return progressSample{}, err
	}
	secs, ok := v.(float64)
	if !ok {
		return progressSample{}, fmt.Errorf("unexpected time-pos type: %T", v)
	}
	return progressSample{position: time.Duration(secs * float64(time.Second)), paused: values[0], eof: values[1], idle: values[2], coreIdle: values[3], buffering: values[4], seeking: values[5]}, nil
}

func isStillImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp":
		return true
	default:
		return false
	}
}

func (p *mpvPlayer) recoverProgress(ctx context.Context, identity uint64, path string, pos time.Duration, action string) bool {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	select {
	case <-ctx.Done():
		return false
	default:
	}
	p.mu.Lock()
	if p.closed || p.intent != identity || p.current != path || p.paused || p.slideshow {
		p.mu.Unlock()
		return false
	}
	if action == "restart" {
		cmd := p.cmd
		if pos <= 0 {
			pos = p.lastPosition
		}
		p.lastPosition = pos
		p.mu.Unlock()
		if cmd == nil || cmd.Process == nil {
			return false
		}
		return cmd.Process.Kill() == nil
	}
	p.mu.Unlock()
	return p.playAtLocked(path, pos) == nil
}

// Retained diagnostics may contain local media paths and require authentication.
// Public readiness contains only fixed reasons, never these log messages.
func boundedMPVLog(prefix, level, message string) string {
	if len(prefix) > 80 {
		prefix = prefix[:80]
	}
	if len(level) > 16 {
		level = level[:16]
	}
	message = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return ' '
		}
		return r
	}, message)
	if len(message) > 400 {
		message = message[:400]
	}
	return "mpv " + level + " [" + prefix + "]: " + message
}

func (p *mpvPlayer) logMPVMessage(event map[string]interface{}) {
	level, _ := event["level"].(string)
	if level != "warn" && level != "error" && level != "fatal" {
		return
	}
	prefix, _ := event["prefix"].(string)
	message, _ := event["text"].(string)
	p.logMu.Lock()
	if time.Since(p.lastLogAt) < 2*time.Second {
		p.logMu.Unlock()
		return
	}
	p.lastLogAt = time.Now()
	p.logMu.Unlock()
	p.diagnostic(boundedMPVLog(prefix, level, message))
}

func (p *mpvPlayer) dialSocketContext(ctx context.Context) (net.Conn, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := net.DialTimeout("unix", p.cfg.SocketPath, 150*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
