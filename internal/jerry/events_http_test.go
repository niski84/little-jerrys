package jerry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/niski84/little-jerrys/internal/eventlog"
)

func TestRetainedEventsHTTPAuthenticationAndRestart(t *testing.T) {
	cfg := Config{MediaRoot: t.TempDir(), StatePath: filepath.Join(t.TempDir(), "state.json"), UsePlayerStub: true, UseGPIOSimulated: true, Version: "fixture-v1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := NewApp(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Store.Update(func(s *State) { s.PasswordIsDefault = false }); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(app)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest("GET", "/api/events/export", nil))
	if denied.Code == 200 {
		t.Fatal("unauthenticated event history exposed")
	}
	login := httptest.NewRecorder()
	app.Auth.Login(login, httptest.NewRequest("POST", "/login", nil))
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no test session")
	}
	app.Events.Error("fixture", "retained error")
	request := httptest.NewRequest("GET", "/api/events/export?after=0&limit=200", nil)
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("export: %d %s", response.Code, response.Body.String())
	}
	var page eventlog.Page
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if !page.Durable || page.Schema != 1 || len(page.Events) == 0 || page.Next != page.Newest {
		t.Fatalf("coverage: %+v", page)
	}
	oldStream := page.Stream
	app.Events.Close()
	app.Events, err = eventlog.NewPersistent(filepath.Join(filepath.Dir(cfg.StatePath), "events.jsonl"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	app.Events.Info("boot", "restart fixture")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var restarted eventlog.Page
	json.Unmarshal(response.Body.Bytes(), &restarted)
	if restarted.Stream != oldStream || restarted.Boot == page.Boot || restarted.Next <= page.Next {
		t.Fatal("restart identity/cursor lost")
	}
	bad := httptest.NewRequest("GET", "/api/events/export?after=-1", nil)
	bad.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, bad)
	if response.Code != 400 {
		t.Fatal("bad cursor accepted")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/health", nil))
	var health map[string]string
	json.Unmarshal(response.Body.Bytes(), &health)
	if health["version"] != "fixture-v1" || health["device_id"] != oldStream {
		t.Fatal("health missing stable identity/version")
	}
}
