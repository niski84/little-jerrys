package schedule

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tt
}

func TestActiveInsideWindow(t *testing.T) {
	// Tuesday 2026-05-05 at 09:30 local — sample weekday morning.
	now := mustTime(t, "2026-05-05T09:30:00-07:00")
	schedules := []Schedule{
		{Name: "Breakfast", Days: []int{1, 2, 3, 4, 5}, StartTime: "07:00", EndTime: "11:30", Enabled: true},
	}
	on, hit := Active(schedules, now)
	if !on {
		t.Fatal("expected schedule to be active at 9:30am Tuesday")
	}
	if hit.Name != "Breakfast" {
		t.Errorf("matched wrong schedule: %q", hit.Name)
	}
}

func TestActiveOutsideWindow(t *testing.T) {
	now := mustTime(t, "2026-05-05T15:00:00-07:00") // 3pm Tuesday
	schedules := []Schedule{
		{Name: "Breakfast", Days: []int{1, 2, 3, 4, 5}, StartTime: "07:00", EndTime: "11:30", Enabled: true},
	}
	on, _ := Active(schedules, now)
	if on {
		t.Fatal("3pm should be off-hours when only Breakfast is configured")
	}
}

func TestActiveSkipsDisabled(t *testing.T) {
	now := mustTime(t, "2026-05-05T09:30:00-07:00")
	schedules := []Schedule{
		{Name: "Breakfast", Days: []int{1, 2, 3, 4, 5}, StartTime: "07:00", EndTime: "11:30", Enabled: false},
	}
	on, _ := Active(schedules, now)
	if on {
		t.Fatal("disabled schedule should not be active")
	}
}

func TestActiveOvernightWindow(t *testing.T) {
	// Late-night service: 22:00 → 02:00 (next day).
	overnight := []Schedule{
		{Name: "Late night", Days: []int{0, 1, 2, 3, 4, 5, 6}, StartTime: "22:00", EndTime: "02:00", Enabled: true},
	}
	cases := []struct {
		ts   string
		want bool
	}{
		{"2026-05-05T23:00:00-07:00", true},  // 11pm
		{"2026-05-06T01:30:00-07:00", true},  // 1:30am next day
		{"2026-05-06T03:00:00-07:00", false}, // 3am — past close
		{"2026-05-05T20:00:00-07:00", false}, // 8pm — before open
	}
	for _, c := range cases {
		got, _ := Active(overnight, mustTime(t, c.ts))
		if got != c.want {
			t.Errorf("at %s: got %v, want %v", c.ts, got, c.want)
		}
	}
}

func TestValidateRequiresName(t *testing.T) {
	s := Schedule{Days: []int{1}, StartTime: "07:00", EndTime: "08:00"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestValidateRequiresAtLeastOneDay(t *testing.T) {
	s := Schedule{Name: "x", StartTime: "07:00", EndTime: "08:00"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for empty days")
	}
}

func TestValidateRejectsBadTime(t *testing.T) {
	s := Schedule{Name: "x", Days: []int{1}, StartTime: "9am", EndTime: "10am"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for non-HH:MM time")
	}
}

func TestEmptyScheduleListMeansAlwaysOn(t *testing.T) {
	on, _ := Active(nil, time.Now())
	if on {
		t.Fatal("Active([]) should return false (caller decides 'always on')")
	}
	// Note: the App.applyDesiredMode treats len(schedules)==0 as always-playback;
	// the schedule package itself just reports "no match found".
}
