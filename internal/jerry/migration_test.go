package jerry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/niski84/little-jerrys/internal/player"
	"github.com/niski84/little-jerrys/internal/schedule"
)

func TestLegacyStatePreservedAndCopied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := `{"admin_password_hash":"preserve-hash","session_key":"preserve-session","password_is_default":false,"last_track":"/opt/jerry/media/s01e01.mp4","last_position_secs":42.5,"volume_level":7,"quiet_mode_enabled":true,"quiet_start":"22:00","quiet_end":"07:00","commercial_campaigns":[{"id":"c1","name":"Spot","enabled":true,"weight":3,"files":["spot.mp4"]}],"schedules":[{"days":[1]}]}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	copy := store.Get()
	copy.CommercialCampaigns[0].Files[0] = "mutated"
	copy.Schedules[0].Days[0] = 6
	if err := store.Update(func(s *State) { s.MinutesPlayed++ }); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st := reopened.Get()
	if st.SessionKey != "preserve-session" || st.AdminPasswordHash != "preserve-hash" || st.PasswordIsDefault || st.LastPositionSecs != 42.5 || st.VolumeLevel != 7 || !st.QuietModeEnabled || st.CommercialCampaigns[0].Files[0] != "spot.mp4" || st.Schedules[0].Days[0] != 1 {
		t.Fatal("legacy state or snapshot isolation lost")
	}
}

func TestConcurrentStateUpdatesPersist(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Update(func(s *State) { s.MinutesPlayed++ }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reopened, err := NewStore(store.path)
	if err != nil || reopened.Get().MinutesPlayed != 30 {
		t.Fatalf("concurrent persistence: %v", err)
	}
}

type quietPlayer struct {
	*player.Stub
	fail  bool
	muted bool
	calls int
}

func (p *quietPlayer) Mute(muted bool) error {
	p.calls++
	if p.fail {
		return errors.New("fixture IPC failure")
	}
	p.muted = muted
	return nil
}

func TestQuietHoursRetryAndOvernight(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err := store.Update(func(s *State) { s.QuietModeEnabled = true }); err != nil {
		t.Fatal(err)
	}
	p := &quietPlayer{Stub: player.NewStub(), fail: true}
	app := &App{Store: store, Player: p}
	night := time.Date(2026, 9, 19, 23, 0, 0, 0, time.Local)
	app.applyQuietMode(night)
	if app.quietActive {
		t.Fatal("failed mute incorrectly recorded as applied")
	}
	p.fail = false
	app.applyQuietMode(night)
	if !p.muted || p.calls != 2 {
		t.Fatal("mute failure was not retried")
	}
	app.applyQuietMode(night.Add(7 * time.Hour))
	if !p.muted {
		t.Fatal("overnight window lost")
	}
	app.applyQuietMode(night.Add(8 * time.Hour))
	if p.muted {
		t.Fatal("quiet hours did not end at 07:00")
	}
}

func TestStartupOnceAndLateEOF(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"s01e01.mp4", "s01e02.mp4", "s01e03.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	app, err := NewApp(context.Background(), Config{MediaRoot: root, StatePath: filepath.Join(t.TempDir(), "state.json"), UsePlayerStub: true, UseGPIOSimulated: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.StartCurrentMode()
	first := app.Current()
	app.StartCurrentMode()
	if app.Current() != first || app.Store.Get().PlayCounts[first] != 1 {
		t.Fatal("duplicate startup load")
	}
	app.advanceAfterEnd("/replaced/old.mp4")
	if app.Current() != first {
		t.Fatal("stale EOF skipped current playback")
	}
	if err := app.Store.Update(func(s *State) { s.Schedules = []schedule.Schedule{} }); err != nil {
		t.Fatal(err)
	}
	app.modeMu.Lock()
	app.mode = ModeOffHours
	app.modeMu.Unlock()
	app.advanceAfterEnd(first)
	if app.Current() != first {
		t.Fatal("off-hours EOF advanced episode rotation")
	}
}
