package jerry

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/niski84/little-jerrys/internal/player"
)

type observedPlayer struct {
	*player.Stub
	health player.PlaybackHealth
}

func (p *observedPlayer) PlaybackHealth() player.PlaybackHealth { return p.health }

func TestPlaybackReadinessDoesNotConfuseLivenessWithProgress(t *testing.T) {
	root := t.TempDir()
	app, err := NewApp(context.Background(), Config{MediaRoot: root, StatePath: filepath.Join(root, "state.json"), UsePlayerStub: true, UseGPIOSimulated: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	p := &observedPlayer{Stub: player.NewStub()}
	app.Player = p
	handler := NewServer(app)
	for _, tc := range []struct {
		state string
		ready bool
	}{
		{"playing", true}, {"paused", true}, {"slideshow", true}, {"static", true},
		{"starting", false}, {"stalled", false}, {"recovering", false}, {"failed", false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			p.health = player.PlaybackHealth{State: tc.state}
			r := httptest.NewRecorder()
			handler.ServeHTTP(r, httptest.NewRequest("GET", "/api/ready", nil))
			want := 503
			if tc.ready {
				want = 200
			}
			if r.Code != want {
				t.Fatalf("ready status=%d, want%d", r.Code, want)
			}
			r = httptest.NewRecorder()
			handler.ServeHTTP(r, httptest.NewRequest("GET", "/api/health", nil))
			var h map[string]string
			if err := json.Unmarshal(r.Body.Bytes(), &h); err != nil {
				t.Fatal(err)
			}
			if r.Code != 200 || h["liveness"] != "ok" || h["playback_state"] != tc.state {
				t.Fatalf("liveness/diagnostics=%s", r.Body.String())
			}
			if !tc.ready && h["status"] != "degraded" {
				t.Fatal("stalled player advertised healthy")
			}
		})
	}
	app.Player = player.NewStub()
	if _, ready := playbackReadiness(app); ready {
		t.Fatal("development stub advertised real readiness")
	}
}
