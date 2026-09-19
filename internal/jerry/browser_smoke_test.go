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

// Opt-in, loopback-only browser verification; never reads deployment config.
func TestMigrationBrowserSmoke(t *testing.T) {
	if os.Getenv("JERRY_BROWSER_TEST") != "1" {
		t.Skip("set JERRY_BROWSER_TEST=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	app, err := NewApp(ctx, Config{MediaRoot: root, StatePath: filepath.Join(root, ".state", "state.json"), UsePlayerStub: true, UseGPIOSimulated: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.StartCurrentMode()
	srv := httptest.NewServer(NewServer(app))
	defer srv.Close()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/migration-browser-smoke.cjs")
	cmd.Env = append(os.Environ(), "JERRY_SMOKE_URL="+srv.URL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser smoke: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
