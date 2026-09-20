package player

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Reproduce the observed failure: IPC responds, pause/EOF/cache are false,
// but the clock never advances. Recovery must reload, not just unpause.
func TestResponsiveFrozenClockReloadsSameTrackAndWaitsForMovement(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "mpv.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var mu sync.Mutex
	position := 465.006
	var loads []string
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				var req struct {
					Command []any `json:"command"`
					ID      int64 `json:"request_id"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				mu.Lock()
				var data any
				if req.Command[0] == "get_property" {
					data = false
					if req.Command[1] == "time-pos" {
						data = position
					}
				}
				if req.Command[0] == "loadfile" {
					loads = append(loads, req.Command[1].(string))
				}
				mu.Unlock()
				_ = json.NewEncoder(conn).Encode(map[string]any{"request_id": req.ID, "error": "success", "data": data})
			}()
		}
	}()
	defer func() { listener.Close(); <-done; wg.Wait() }()
	p := &mpvPlayer{cfg: Config{SocketPath: sock}, current: "episode.mkv", intent: 1}
	tracker := progressTracker{stallAfter: 30 * time.Second, bufferGrace: 120 * time.Second}
	var budget progressBudget
	now := time.Now()
	p.checkProgress(context.Background(), now, &tracker, &budget)
	p.checkProgress(context.Background(), now.Add(31*time.Second), &tracker, &budget)
	mu.Lock()
	gotLoads := append([]string(nil), loads...)
	mu.Unlock()
	if len(gotLoads) != 1 || gotLoads[0] != "episode.mkv" {
		t.Fatalf("wrong recovery loads: %v", gotLoads)
	}
	if h := p.PlaybackHealth(); h.State != "recovering" || h.RecoveryCount != 1 {
		t.Fatalf("recovery advertised healthy prematurely: %+v", h)
	}
	p.checkProgress(context.Background(), now.Add(36*time.Second), &tracker, &budget)
	mu.Lock()
	position += 2
	mu.Unlock()
	p.checkProgress(context.Background(), now.Add(41*time.Second), &tracker, &budget)
	if h := p.PlaybackHealth(); h.State != "playing" {
		t.Fatalf("observed progress not recognized: %+v", h)
	}
	if p.recoverProgress(context.Background(), 2, "episode.mkv", 0, "reload") {
		t.Fatal("stale intent allowed to replace playback")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.recoverProgress(context.Background(), 1, "episode.mkv", 0, "reload") {
		t.Fatal("recovery allowed after close")
	}
}
