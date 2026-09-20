package jerry

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionRequiresRealPlayer(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no mpv, without touching the host installation
	root := t.TempDir()
	app, err := NewApp(context.Background(), Config{
		MediaRoot: root, StatePath: filepath.Join(root, "state.json"),
		UseGPIOSimulated: true,
	})
	if app != nil {
		app.Close()
		t.Fatal("production silently accepted a fake player")
	}
	if err == nil || !strings.Contains(err.Error(), "production playback unavailable") {
		t.Fatalf("expected visible player startup failure, got %v", err)
	}
}
