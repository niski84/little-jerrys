package jerry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPlayCurrentResumesSavedTrack verifies the headline behavior: when the
// Pi powers off mid-episode and boots back up, mpv resumes the same file at
// the same position rather than restarting cold.
func TestPlayCurrentResumesSavedTrack(t *testing.T) {
	dir := t.TempDir()
	episode := filepath.Join(dir, "ep.mp4")
	if err := os.WriteFile(episode, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	cfg := Config{
		MediaRoot:        dir,
		StatePath:        statePath,
		UsePlayerStub:    true,
		UseGPIOSimulated: true,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app, err := NewApp(ctx, cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Close()

	// Pretend we ran for a while: save a "last track + position" by hand.
	const wantPos = 42.0
	_ = app.Store.Update(func(s *State) {
		s.LastTrack = episode
		s.LastPositionSecs = wantPos
		s.PasswordIsDefault = false
	})

	// Now boot — PlayCurrent should resume rather than start fresh.
	if err := app.PlayCurrent(); err != nil {
		t.Fatalf("PlayCurrent: %v", err)
	}

	got := app.Player.Status()
	if got.NowPlaying != episode {
		t.Errorf("now-playing = %q, want %q", got.NowPlaying, episode)
	}

	// Stub Player records the start position via PlayAt — its Position()
	// should report ~wantPos right after the call (modulo small elapsed).
	pos, err := app.Player.Position()
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	gotPos := pos.Seconds()
	if gotPos < wantPos-1 || gotPos > wantPos+1 {
		t.Errorf("Position() = %.2f, want ~%.0f", gotPos, wantPos)
	}
}

// TestPlayCurrentFallsBackWhenSavedFileMissing covers the USB-walked-off case:
// state references a file that no longer exists. PlayCurrent must not crash
// and must start a fresh random episode.
func TestPlayCurrentFallsBackWhenSavedFileMissing(t *testing.T) {
	dir := t.TempDir()
	episode := filepath.Join(dir, "alive.mp4")
	if err := os.WriteFile(episode, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	cfg := Config{
		MediaRoot:        dir,
		StatePath:        statePath,
		UsePlayerStub:    true,
		UseGPIOSimulated: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := NewApp(ctx, cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Close()

	_ = app.Store.Update(func(s *State) {
		s.LastTrack = filepath.Join(dir, "ghost.mp4") // doesn't exist
		s.LastPositionSecs = 999
	})

	if err := app.PlayCurrent(); err != nil {
		t.Fatalf("PlayCurrent should fall back, got: %v", err)
	}

	if got := app.Player.Status().NowPlaying; got != episode {
		t.Errorf("expected fallback to alive episode, got %q", got)
	}
}

// TestPositionSaverPersistsPlayhead ensures the periodic ticker actually
// writes the playhead — the whole feature depends on this writing to disk
// before the Pi loses power.
func TestPositionSaverPersistsPlayhead(t *testing.T) {
	dir := t.TempDir()
	episode := filepath.Join(dir, "ep.mp4")
	_ = os.WriteFile(episode, []byte("x"), 0o644)

	statePath := filepath.Join(t.TempDir(), "state.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := NewApp(ctx, Config{
		MediaRoot: dir, StatePath: statePath, UsePlayerStub: true, UseGPIOSimulated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	_ = app.PlayNext()
	// Let the stub player accumulate some elapsed time.
	time.Sleep(150 * time.Millisecond)

	// Trigger a save manually rather than waiting for the 10s ticker.
	app.savePosition()

	st := app.Store.Get()
	if st.LastTrack == "" {
		t.Fatal("LastTrack not persisted")
	}
	if st.LastPositionSecs <= 0 {
		t.Errorf("LastPositionSecs = %.3f, want > 0", st.LastPositionSecs)
	}
}
