package player

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProgressTrackerHonorsIntentAndPlaybackStates(t *testing.T) {
	now := time.Now()
	tracker := progressTracker{stallAfter: 30 * time.Second, bufferGrace: 120 * time.Second}
	tracker.observe(now, 1, progressSample{position: time.Minute})
	cases := []struct {
		name   string
		sample progressSample
		state  string
	}{
		{"paused", progressSample{paused: true}, "paused"},
		{"slideshow", progressSample{slideshow: true}, "slideshow"},
		{"static", progressSample{static: true}, "static"},
		{"eof", progressSample{eof: true}, "eof"},
		{"seeking", progressSample{seeking: true}, "seeking"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eligible, state, stalled, _ := tracker.observe(now.Add(time.Hour), 1, tc.sample)
			if eligible || stalled || state != tc.state {
				t.Fatalf("observe = eligible %v state %q stalled %v", eligible, state, stalled)
			}
		})
	}
}

func TestProgressTrackerBufferGraceAndCoreIdle(t *testing.T) {
	now := time.Now()
	tracker := progressTracker{stallAfter: 30 * time.Second, bufferGrace: 120 * time.Second}
	base := progressSample{position: time.Minute, buffering: true, coreIdle: true}
	if eligible, state, stalled, _ := tracker.observe(now, 1, base); eligible || state != "buffering" || stalled {
		t.Fatalf("buffer grace start: %v %q %v", eligible, state, stalled)
	}
	if eligible, state, stalled, _ := tracker.observe(now.Add(119*time.Second), 1, base); eligible || state != "buffering" || stalled {
		t.Fatalf("buffer grace active: %v %q %v", eligible, state, stalled)
	}
	if eligible, state, stalled, _ := tracker.observe(now.Add(121*time.Second), 1, base); !eligible || state != "recovering" || !stalled {
		t.Fatalf("expired buffering grace should expose stall: %v %q %v", eligible, state, stalled)
	}
}

func TestProgressTrackerRecoveryWaitsForActualMovement(t *testing.T) {
	now := time.Now()
	tracker := progressTracker{stallAfter: 30 * time.Second, bufferGrace: 120 * time.Second, awaitingProgress: true}
	s := progressSample{position: time.Minute}
	tracker.observe(now, 4, s)
	if eligible, state, stalled, _ := tracker.observe(now.Add(time.Second), 4, s); !eligible || state != "recovering" || stalled {
		t.Fatalf("recovery before movement: %v %q %v", eligible, state, stalled)
	}
	if eligible, state, stalled, moved := tracker.observe(now.Add(2*time.Second), 4, progressSample{position: time.Minute + time.Second}); !eligible || state != "playing" || stalled || !moved {
		t.Fatalf("recovery after movement: %v %q %v moved=%v", eligible, state, stalled, moved)
	}
}

func TestProgressBudgetIsHardRollingWindow(t *testing.T) {
	var b progressBudget
	start := time.Now()
	for i := 0; i < progressMaxAttempts; i++ {
		if _, ok := b.take(start.Add(time.Duration(i) * time.Minute)); !ok {
			t.Fatalf("attempt %d unexpectedly denied", i+1)
		}
	}
	if action, ok := b.take(start.Add(4 * time.Minute)); ok || action != "" {
		t.Fatal("brief recovery progress must not clear the hard retry budget")
	}
	if action, ok := b.take(start.Add(13 * time.Minute)); !ok || action != "reload" {
		t.Fatalf("expired window did not reset budget: %q %v", action, ok)
	}
}

func TestProgressQueryHonorsDeadlineAndClose(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "mpv.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			buf := make([]byte, 512)
			_, _ = conn.Read(buf)
			<-closed
		}
	}()
	p := &mpvPlayer{cfg: Config{SocketPath: sock}}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	started := time.Now()
	_, err = p.queryContext(ctx, "get_property", "time-pos")
	cancel()
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("query did not honor bounded context: err=%v elapsed=%s", err, time.Since(started))
	}
	close(closed)

	loopCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.progressLoop(loopCtx); close(done) }()
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("progress monitor did not stop on close")
	}
}

func TestPlaybackHealthSnapshotAndDiagnosticBounds(t *testing.T) {
	p := &mpvPlayer{}
	p.setPlaybackHealth("playing", "", time.Now().Add(-time.Second), 2)
	h := p.PlaybackHealth()
	if h.State != "playing" || h.RecoveryCount != 2 || h.LastProgressAge < time.Second {
		t.Fatalf("unexpected health snapshot: %+v", h)
	}
	line := boundedMPVLog("vo", "warn", strings.Repeat("x", 500))
	if len(line) > 430 {
		t.Fatalf("diagnostic exceeded bound: %d", len(line))
	}
}
