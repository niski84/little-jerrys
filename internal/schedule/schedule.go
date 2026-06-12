// Package schedule decides whether the appliance should currently be playing
// video or showing the fan-art slideshow. Tony configures one or more
// time-of-day windows ("Breakfast 7-11:30, Dinner 5-9"). Outside those
// windows the box is "off hours" — fan art on the TV instead of episodes.
//
// "Pauses between playing" is just a gap between two schedule entries — no
// special concept needed for it.
package schedule

import (
	"fmt"
	"strings"
	"time"
)

// Schedule is one named time-of-day window. Days uses Go's time.Weekday
// numbering (0=Sunday, 6=Saturday). StartTime/EndTime are 24h "HH:MM".
type Schedule struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Days      []int   `json:"days"`
	StartTime string  `json:"start_time"`
	EndTime   string  `json:"end_time"`
	Enabled   bool    `json:"enabled"`
}

// Validate returns a human-readable error if the schedule is malformed.
func (s Schedule) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(s.Days) == 0 {
		return fmt.Errorf("pick at least one day of the week")
	}
	for _, d := range s.Days {
		if d < 0 || d > 6 {
			return fmt.Errorf("invalid day %d (0=Sun..6=Sat)", d)
		}
	}
	if _, err := parseClock(s.StartTime); err != nil {
		return fmt.Errorf("start time: %w", err)
	}
	if _, err := parseClock(s.EndTime); err != nil {
		return fmt.Errorf("end time: %w", err)
	}
	return nil
}

// Active returns the matching schedule (if any) for the given moment. The
// boolean reports whether *some* schedule is active. If multiple overlap,
// the first match in slice order wins.
func Active(schedules []Schedule, now time.Time) (bool, Schedule) {
	dow := int(now.Weekday())
	mins := now.Hour()*60 + now.Minute()
	for _, s := range schedules {
		if !s.Enabled {
			continue
		}
		if !containsInt(s.Days, dow) {
			continue
		}
		start, err := parseClock(s.StartTime)
		if err != nil {
			continue
		}
		end, err := parseClock(s.EndTime)
		if err != nil {
			continue
		}
		if mins >= start && mins < end {
			return true, s
		}
		// Overnight window (e.g. 22:00 → 02:00) — end-of-day < start.
		if end < start && (mins >= start || mins < end) {
			return true, s
		}
	}
	return false, Schedule{}
}

// DayLabels are short human-readable labels for the form UI.
var DayLabels = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// parseClock returns minutes-since-midnight for "HH:MM".
func parseClock(s string) (int, error) {
	s = strings.TrimSpace(s)
	if len(s) < 4 || len(s) > 5 || strings.Count(s, ":") != 1 {
		return 0, fmt.Errorf("expected HH:MM, got %q", s)
	}
	parts := strings.SplitN(s, ":", 2)
	var h, m int
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return 0, fmt.Errorf("bad hour %q", parts[0])
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return 0, fmt.Errorf("bad minute %q", parts[1])
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("out of range")
	}
	return h*60 + m, nil
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
