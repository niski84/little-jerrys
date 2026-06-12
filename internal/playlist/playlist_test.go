package playlist

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// stage builds a fake media root with episodes (named "sNNeNN_label.mp4"
// so episodeKeyFromPath parses them) and optional commercials. Cleanup
// is handled by t.TempDir.
//
// episodes is a map of S/E → label, so callers can pick the keys they
// want to drive weight/block scenarios. Pass labels="" if you don't care.
func stage(t *testing.T, episodes map[[2]int]string, commercials []string) string {
	t.Helper()
	dir := t.TempDir()
	for se, label := range episodes {
		name := fmt.Sprintf("s%02de%02d_%s.mp4", se[0], se[1], label)
		writeFile(t, filepath.Join(dir, name), "x")
	}
	if len(commercials) > 0 {
		commDir := filepath.Join(dir, "commercials")
		if err := os.MkdirAll(commDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range commercials {
			writeFile(t, filepath.Join(commDir, name+".mp4"), "x")
		}
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeSeasonOne stages 5 episodes from S01 ("S01E01..S01E05") plus optional
// commercials. The most common shape for the weight/blacklist tests.
func makeSeasonOne(t *testing.T, commercials []string) string {
	return stage(t, map[[2]int]string{
		{1, 1}: "pilot",
		{1, 2}: "stake",
		{1, 3}: "robbery",
		{1, 4}: "male_unbonding",
		{1, 5}: "stock_tip",
	}, commercials)
}

func TestScanFindsEpisodesAndCommercials(t *testing.T) {
	root := makeSeasonOne(t, []string{"ad_1", "ad_2"})
	e := New(root)
	if err := e.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	stats := e.Stats()
	if stats.TotalEpisodes != 5 {
		t.Errorf("want 5 episodes, got %d", stats.TotalEpisodes)
	}
	if stats.CommercialsLoaded != 2 {
		t.Errorf("want 2 commercials, got %d", stats.CommercialsLoaded)
	}
}

// TestEpisodeKeyParsing — the heart of the S/E-keyed flow. If this regresses,
// every weight + block lookup silently no-ops, and the user thinks they're
// blocking episodes that still play.
func TestEpisodeKeyParsing(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/media/usb/s01e01_pilot.mp4", "S01E01"},
		{"/media/usb/S02E11_chinese_restaurant.mkv", "S02E11"},
		{"/media/usb/seinfeld.s09e10.chinese_woman.mp4", "S09E10"},
		{"/media/usb/random.mp4", ""}, // no parseable code
		{"/media/usb/episode_5.mp4", ""},
	}
	for _, c := range cases {
		got := episodeKeyFromPath(c.path)
		if got != c.want {
			t.Errorf("episodeKeyFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestTrueRandomNoEarlyRepeats — every episode plays before any repeats.
// The whole point of the project. Single cycle through the catalog must
// produce 5 distinct outputs for a 5-episode catalog.
func TestTrueRandomNoEarlyRepeats(t *testing.T) {
	root := makeSeasonOne(t, nil)
	e := New(root)
	if err := e.Scan(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		next, err := e.Next()
		if err != nil {
			t.Fatalf("next #%d: %v", i, err)
		}
		if seen[next.Path] {
			t.Fatalf("episode %s repeated within first cycle (i=%d)", filepath.Base(next.Path), i)
		}
		seen[next.Path] = true
	}
}

// TestBlacklistExcludesEpisode — block S01E03 by SNNENN key, verify it
// never surfaces. This is the actual user-facing flow: the admin UI
// posts to /episodes/{S}/{E}/blocked which appends "S01E03" to State.
func TestBlacklistExcludesEpisode(t *testing.T) {
	root := makeSeasonOne(t, nil)
	e := New(root)
	_ = e.Scan()
	e.SetBlacklist([]string{"S01E03"})

	// 12 picks > 5 episodes → at least one full cycle. If the block fails,
	// S01E03's path will appear at least once.
	wantBlockedSubstr := "s01e03_robbery"
	for i := 0; i < 12; i++ {
		next, err := e.Next()
		if err != nil {
			t.Fatalf("next #%d: %v", i, err)
		}
		base := filepath.Base(next.Path)
		if containsCaseInsensitive(base, wantBlockedSubstr) {
			t.Fatalf("blocked S01E03 surfaced anyway: %s (i=%d)", base, i)
		}
	}
}

// TestUnblockReturnsEpisodeToRotation — the round-trip: block then unblock.
// Mirrors what the user does on the episode detail page.
func TestUnblockReturnsEpisodeToRotation(t *testing.T) {
	root := makeSeasonOne(t, nil)
	e := New(root)
	_ = e.Scan()
	e.SetBlacklist([]string{"S01E02"})

	// Confirm S01E02 is excluded across a cycle.
	for i := 0; i < 8; i++ {
		next, _ := e.Next()
		if containsCaseInsensitive(filepath.Base(next.Path), "s01e02_") {
			t.Fatalf("expected S01E02 blocked at i=%d", i)
		}
	}
	// Unblock: pass empty list. SNNENN entry should be cleared from the map.
	e.SetBlacklist(nil)

	// S01E02 should now appear within a few cycles.
	for i := 0; i < 30; i++ {
		next, _ := e.Next()
		if containsCaseInsensitive(filepath.Base(next.Path), "s01e02_") {
			return // success — the episode is back in rotation
		}
	}
	t.Fatal("after unblock S01E02 never returned across 30 picks — block didn't release")
}

// TestWeightedEpisodeRunsMoreOften — the real test of the weighting. With
// weight 10 on one episode and weight 1 on four others, the heavy one
// should appear in roughly 10/(10+4) ≈ 71% of slots. Generous lower bound.
func TestWeightedEpisodeRunsMoreOften(t *testing.T) {
	root := makeSeasonOne(t, nil)
	e := New(root)
	_ = e.Scan()
	e.SetEpisodeWeight("S01E01", 10)

	hits := 0
	const trials = 300
	for i := 0; i < trials; i++ {
		next, _ := e.Next()
		if containsCaseInsensitive(filepath.Base(next.Path), "s01e01_") {
			hits++
		}
	}
	// Expected ~71% (213/300). Anything under 50% is a real regression.
	if hits < trials/2 {
		t.Errorf("S01E01 weight=10 underrepresented: %d/%d (≈%d%%)",
			hits, trials, 100*hits/trials)
	}
}

// TestWeightTiersOrderTheDistribution — three episodes at three different
// weights should appear in the right relative ratios.
func TestWeightTiersOrderTheDistribution(t *testing.T) {
	root := stage(t, map[[2]int]string{
		{1, 1}: "lo",
		{1, 2}: "mid",
		{1, 3}: "hi",
	}, nil)
	e := New(root)
	_ = e.Scan()
	e.SetEpisodeWeight("S01E02", 3)
	e.SetEpisodeWeight("S01E03", 6)
	// S01E01 left at default weight 1.

	counts := map[string]int{"S01E01": 0, "S01E02": 0, "S01E03": 0}
	const trials = 500
	for i := 0; i < trials; i++ {
		next, _ := e.Next()
		key := episodeKeyFromPath(next.Path)
		counts[key]++
	}

	// hi > mid > lo by a comfortable margin.
	if counts["S01E03"] <= counts["S01E02"] {
		t.Errorf("S01E03 (w=6) should beat S01E02 (w=3): %d vs %d", counts["S01E03"], counts["S01E02"])
	}
	if counts["S01E02"] <= counts["S01E01"] {
		t.Errorf("S01E02 (w=3) should beat S01E01 (w=1): %d vs %d", counts["S01E02"], counts["S01E01"])
	}
	t.Logf("weight 1=%d  3=%d  6=%d (of %d trials)",
		counts["S01E01"], counts["S01E02"], counts["S01E03"], trials)
}

// TestUnparseableFilenameIgnoresWeightSilently — defensive: documents the
// behavior when a media file is named without an SNNENN prefix. The
// engine treats it as weight=1 (and unblockable), regardless of what's in
// the map. Caller is expected to either rename the file or accept the
// silent fallback.
func TestUnparseableFilenameIgnoresWeightSilently(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "weirdname.mp4"), "x") // no s/e prefix
	e := New(dir)
	_ = e.Scan()
	e.SetEpisodeWeight("S01E01", 99) // shouldn't apply to weirdname.mp4

	// We expect "weirdname" to play every time (it's the only file).
	for i := 0; i < 5; i++ {
		next, _ := e.Next()
		if filepath.Base(next.Path) != "weirdname.mp4" {
			t.Fatalf("expected weirdname.mp4, got %s", filepath.Base(next.Path))
		}
	}
}

// TestCommercialBreakInsertsAfterThreshold — broadcast scheduler: with
// 22-min episode default duration and 22-min break threshold, every
// episode should trigger a 2-ad break.
func TestCommercialBreakInsertsAfterThreshold(t *testing.T) {
	root := makeSeasonOne(t, []string{"ad1", "ad2"})
	e := New(root)
	_ = e.Scan()
	e.EnableCommercials(true)
	e.SetBreakSchedule(22, 2)

	commsSeen := 0
	episodesSeen := 0
	for i := 0; i < 10; i++ {
		next, err := e.Next()
		if err != nil {
			t.Fatalf("next #%d: %v", i, err)
		}
		if next.Kind == KindCommercial {
			commsSeen++
		} else {
			episodesSeen++
		}
	}
	if commsSeen == 0 {
		t.Errorf("no commercials inserted across 10 picks (eps=%d comms=%d)", episodesSeen, commsSeen)
	}
}

func TestCommercialDisabledMeansNoBreaks(t *testing.T) {
	root := stage(t, map[[2]int]string{{1, 1}: "only"}, []string{"ad1"})
	e := New(root)
	_ = e.Scan()
	e.SetBreakSchedule(1, 5) // would fire constantly if enabled

	for i := 0; i < 20; i++ {
		next, _ := e.Next()
		if next.Kind == KindCommercial {
			t.Fatalf("commercial leaked through with EnableCommercials=false: %s", next.Path)
		}
	}
}

func TestLoopGuardSurfacesHotEpisodes(t *testing.T) {
	root := stage(t, map[[2]int]string{{1, 1}: "only"}, nil) // forces repeats
	e := New(root)
	_ = e.Scan()
	for i := 0; i < 5; i++ {
		_, _ = e.Next()
	}
	hot := e.LoopGuard(2)
	if len(hot) == 0 {
		t.Fatal("loop guard didn't surface the over-played episode")
	}
}

func containsCaseInsensitive(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	hl := []byte(haystack)
	nl := []byte(needle)
	for i := range hl {
		if hl[i] >= 'A' && hl[i] <= 'Z' {
			hl[i] += 'a' - 'A'
		}
	}
	for i := range nl {
		if nl[i] >= 'A' && nl[i] <= 'Z' {
			nl[i] += 'a' - 'A'
		}
	}
	for i := 0; i+len(nl) <= len(hl); i++ {
		match := true
		for j := 0; j < len(nl); j++ {
			if hl[i+j] != nl[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
