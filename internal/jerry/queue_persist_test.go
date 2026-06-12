package jerry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestQueueItemsRestoredFromState is the headline regression for the
// reboot-loses-queue bug we fixed. Drop a state.json with QueueItems on
// disk → NewApp must restore them into the engine before any user action.
func TestQueueItemsRestoredFromState(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"s01e01", "s01e02", "s01e03"} {
		if err := os.WriteFile(filepath.Join(dir, n+"_x.mp4"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	saved := map[string]interface{}{
		"password_is_default": false,
		"queue_items": []map[string]interface{}{
			{"kind": "episode", "path": filepath.Join(dir, "s01e02_x.mp4"), "est_secs": 1320},
			{"kind": "episode", "path": filepath.Join(dir, "s01e01_x.mp4"), "est_secs": 1320},
			{"kind": "commercial", "path": filepath.Join(dir, "ad.mp4"), "est_secs": 30},
			{"kind": "episode", "path": filepath.Join(dir, "s01e03_x.mp4"), "est_secs": 1320},
		},
	}
	data, _ := json.Marshal(saved)
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := NewApp(ctx, Config{
		MediaRoot:        dir,
		StatePath:        statePath,
		UsePlayerStub:    true,
		UseGPIOSimulated: true,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Close()

	got := app.Playlist.Peek(0)
	if len(got) != 4 {
		t.Fatalf("expected 4 restored items, got %d", len(got))
	}
	wantOrder := []string{"s01e02_x.mp4", "s01e01_x.mp4", "ad.mp4", "s01e03_x.mp4"}
	for i, want := range wantOrder {
		if !strings.HasSuffix(got[i].Path, want) {
			t.Errorf("queue[%d]: got %q, want suffix %q", i, got[i].Path, want)
		}
	}
}

// TestSnapshotQueuePersistsAfterPlayNext — the consume-and-persist loop.
// Each PlayNext must shrink the saved QueueItems by one so a power-off
// after track 5 boots back into track 6, not track 1.
func TestSnapshotQueuePersistsAfterPlayNext(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"s02e01", "s02e02", "s02e03"} {
		_ = os.WriteFile(filepath.Join(dir, n+"_x.mp4"), []byte("x"), 0o644)
	}
	statePath := filepath.Join(t.TempDir(), "state.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app, err := NewApp(ctx, Config{
		MediaRoot:        dir,
		StatePath:        statePath,
		UsePlayerStub:    true,
		UseGPIOSimulated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Force the auto-regen path so we have a deterministic queue size.
	app.Playlist.Regenerate()
	app.SnapshotQueue()
	startSize := len(app.Playlist.Peek(0))
	if startSize == 0 {
		t.Fatal("expected non-empty queue after Regenerate + Snapshot")
	}

	// PlayNext should consume one item AND persist the new (shorter) queue.
	if err := app.PlayNext(); err != nil {
		t.Fatalf("PlayNext: %v", err)
	}
	got := readSavedQueueLen(t, statePath)
	if got != startSize-1 {
		t.Errorf("after one PlayNext: saved queue len = %d, want %d", got, startSize-1)
	}

	if err := app.PlayNext(); err != nil {
		t.Fatalf("PlayNext #2: %v", err)
	}
	got = readSavedQueueLen(t, statePath)
	if got != startSize-2 {
		t.Errorf("after two PlayNexts: saved queue len = %d, want %d", got, startSize-2)
	}
}

// TestEmptyQueueAutoRegeneratesOnBoot — when the saved state is empty but
// media exists, NewApp must populate the queue itself so the dashboard
// doesn't render "Hit Regenerate" on first paint.
func TestEmptyQueueAutoRegeneratesOnBoot(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"s03e01", "s03e02"} {
		_ = os.WriteFile(filepath.Join(dir, n+"_x.mp4"), []byte("x"), 0o644)
	}
	statePath := filepath.Join(t.TempDir(), "state.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := NewApp(ctx, Config{
		MediaRoot:        dir,
		StatePath:        statePath,
		UsePlayerStub:    true,
		UseGPIOSimulated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	if got := len(app.Playlist.Peek(0)); got == 0 {
		t.Fatal("expected NewApp to auto-regenerate queue when state is empty + media available")
	}
}

// readSavedQueueLen reads the JSON file on disk and returns the count of
// queue_items currently persisted. Asserts the test-relevant invariant
// directly against the file rather than going through Store.Get.
func readSavedQueueLen(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var s struct {
		QueueItems []map[string]interface{} `json:"queue_items"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	return len(s.QueueItems)
}
