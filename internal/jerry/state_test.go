package jerry

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/niski84/little-jerrys/internal/schedule"
)

// TestStateRoundTripThroughRestart is the regression test for the user's
// concern: every persisted field must survive Save → reopen Store. If
// anything you see in the admin UI ever stops sticking after a restart,
// add the field here.
func TestStateRoundTripThroughRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	// First "boot" — write everything.
	{
		s, err := NewStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Update(func(st *State) {
			// Blacklist + Weights are SNNENN-keyed since the engine
			// refactor — paths get pruned by the migration in load().
			st.Blacklist = []string{"S01E01", "S01E02"}
			st.Weights = map[string]int{"S01E03": 3, "S01E04": 7}
			st.CommercialsEnabled = true
			st.CommercialWeights = map[string]int{"/usb/commercials/ad1.mp4": 2}
			st.CommercialBlacklist = []string{"/usb/commercials/ad_bad.mp4"}
			st.BreakAfterMinutes = 18
			st.CommercialsPerBreak = 3
			st.AdminPasswordHash = "$2a$10$abcdef"
			st.PasswordIsDefault = false
			st.WebhookOutbound = []string{"http://hook.example/jerry"}
			st.LoopGuardThreshold = 9
			st.LastTrack = "/usb/ep_a.mp4"
			st.LastPositionSecs = 123.456
			st.Schedules = []schedule.Schedule{
				{ID: "x1", Name: "Breakfast", Days: []int{1, 2, 3, 4, 5},
					StartTime: "07:00", EndTime: "11:30", Enabled: true},
				{ID: "x2", Name: "Dinner", Days: []int{0, 6},
					StartTime: "17:00", EndTime: "21:00", Enabled: false},
			}
			st.FanArtEnabled = true
			st.FanArtSecsPerImage = 12
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	// Second "boot" — reopen and verify nothing changed.
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := s2.Get()

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Blacklist", got.Blacklist, []string{"S01E01", "S01E02"}},
		{"Weights[S01E03]", got.Weights["S01E03"], 3},
		{"Weights[S01E04]", got.Weights["S01E04"], 7},
		{"CommercialsEnabled", got.CommercialsEnabled, true},
		{"CommercialWeights[ad1]", got.CommercialWeights["/usb/commercials/ad1.mp4"], 2},
		{"CommercialBlacklist", got.CommercialBlacklist, []string{"/usb/commercials/ad_bad.mp4"}},
		{"BreakAfterMinutes", got.BreakAfterMinutes, 18},
		{"CommercialsPerBreak", got.CommercialsPerBreak, 3},
		{"AdminPasswordHash", got.AdminPasswordHash, "$2a$10$abcdef"},
		{"PasswordIsDefault", got.PasswordIsDefault, false},
		{"WebhookOutbound", got.WebhookOutbound, []string{"http://hook.example/jerry"}},
		{"LoopGuardThreshold", got.LoopGuardThreshold, 9},
		{"LastTrack", got.LastTrack, "/usb/ep_a.mp4"},
		{"LastPositionSecs", got.LastPositionSecs, 123.456},
		{"FanArtEnabled", got.FanArtEnabled, true},
		{"FanArtSecsPerImage", got.FanArtSecsPerImage, 12},
		{"len(Schedules)", len(got.Schedules), 2},
		{"Schedules[0].ID", got.Schedules[0].ID, "x1"},
		{"Schedules[0].Name", got.Schedules[0].Name, "Breakfast"},
		{"Schedules[0].Days", got.Schedules[0].Days, []int{1, 2, 3, 4, 5}},
		{"Schedules[0].Enabled", got.Schedules[0].Enabled, true},
		{"Schedules[1].Enabled", got.Schedules[1].Enabled, false},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %#v, want %#v", c.name, c.got, c.want)
		}
	}
}

// TestPartialUpdateDoesNotClobberOtherFields covers the subtle bug class the
// user was worried about: writing through one mutator must leave fields not
// touched by that mutator alone. (Catches a refactor that accidentally
// returned a fresh State{} from Update().)
func TestPartialUpdateDoesNotClobberOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := NewStore(path)

	// Seed two unrelated fields. Weights uses the post-refactor SNNENN key.
	_ = s.Update(func(st *State) {
		st.Weights = map[string]int{"S01E01": 4}
		st.LoopGuardThreshold = 6
	})
	// Touch only one of them.
	_ = s.Update(func(st *State) {
		st.LoopGuardThreshold = 11
	})

	// Reopen — both should still be present.
	s2, _ := NewStore(path)
	got := s2.Get()
	if got.LoopGuardThreshold != 11 {
		t.Errorf("loop guard: got %d, want 11", got.LoopGuardThreshold)
	}
	if w := got.Weights["S01E01"]; w != 4 {
		t.Errorf("weight: got %d, want 4 (partial update clobbered the unrelated field)", w)
	}
}
