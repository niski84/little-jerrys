package jerry

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Verifies the actual worker -> authenticated HTTP -> rendered Settings flow
// on temporary media, with no access to the deployed Pi or real state.
func TestLibraryImportBrowser(t *testing.T) {
	if os.Getenv("JERRY_BROWSER_TEST") != "1" {
		t.Skip("set JERRY_BROWSER_TEST=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	source, local, state := t.TempDir(), t.TempDir(), t.TempDir()
	fixture := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x64:d=1", "-c:v", "mpeg4", filepath.Join(source, "Seinfeld - 9x12 - The Reverse Peephole.mp4"))
	if out, err := fixture.CombinedOutput(); err != nil {
		t.Fatalf("fixture %v %s", err, out)
	}
	app, err := NewApp(ctx, Config{MediaRoot: local, ImportRoot: source, StatePath: filepath.Join(state, "state.json"), UsePlayerStub: true, UseGPIOSimulated: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(NewServer(app))
	defer srv.Close()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/library-browser-smoke.cjs")
	cmd.Env = append(os.Environ(), "JERRY_SMOKE_URL="+srv.URL, "JERRY_LIBRARY_PATH="+local)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
