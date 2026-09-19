package jerry

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/niski84/little-jerrys/internal/eventlog"
	"github.com/niski84/little-jerrys/internal/player"
	"github.com/niski84/little-jerrys/internal/playlist"
)

type shutdownPlayer struct {
	*player.Stub
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	closed  bool
	plays   []string
}

func (p *shutdownPlayer) Play(path string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("play after close")
	}
	p.plays = append(p.plays, path)
	p.mu.Unlock()
	if path == "waiting.png" {
		close(p.entered)
		<-p.release
	}
	return nil
}

func (p *shutdownPlayer) Position() (time.Duration, error) { return 12 * time.Second, nil }

func (p *shutdownPlayer) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return nil
}

func TestCloseWaitsForEOFAdvanceBeforeSavingAndClosingPlayer(t *testing.T) {
	root := t.TempDir()
	next := filepath.Join(root, "next.mp4")
	if err := os.WriteFile(next, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := &shutdownPlayer{Stub: player.NewStub(), entered: make(chan struct{}), release: make(chan struct{})}
	app := &App{
		Store: store, Player: p, Playlist: playlist.New(root), Events: eventlog.New(20),
		waitingPath: "waiting.png", current: "ended.mp4", mode: ModePlayback,
	}
	app.Playlist.LoadQueue([]playlist.Item{{Kind: playlist.KindEpisode, Path: next}})

	advanceDone := make(chan struct{})
	go func() { defer close(advanceDone); app.advanceAfterEnd("ended.mp4") }()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("EOF advance did not reach interstitial")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- app.Close() }()
	deadline := time.After(time.Second)
	for !app.closing.Load() {
		select {
		case <-deadline:
			t.Fatal("Close did not begin")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while EOF advance was blocked: %v", err)
	default:
	}
	close(p.release)
	select {
	case <-advanceDone:
	case <-time.After(time.Second):
		t.Fatal("EOF advance did not finish")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	if got := store.Get(); got.LastTrack != "ended.mp4" || got.LastPositionSecs != 12 {
		t.Fatalf("final position not saved before close: %+v", got)
	}
	if err := app.PlayNext(); err == nil {
		t.Fatal("PlayNext accepted after shutdown")
	}
	app.advanceAfterEnd("ended.mp4")
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.plays) != 1 || p.plays[0] != "waiting.png" {
		t.Fatalf("advance loaded playback after shutdown started: %v", p.plays)
	}
}
