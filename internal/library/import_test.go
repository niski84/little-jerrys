package library

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niski84/little-jerrys/internal/episodeid"
	"github.com/niski84/little-jerrys/internal/tmdb"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := NewCatalog(tmdb.NewCache(tmdb.EmbeddedFS))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRealCatalogMatching(t *testing.T) {
	c := testCatalog(t)
	if len(c.Episodes) != 180 {
		t.Fatalf("catalog entries=%d", len(c.Episodes))
	}
	for _, tc := range []struct {
		name    string
		seconds float64
		want    []episodeid.ID
		bad     bool
	}{
		{"SEINFELD.s01e01.The.Seinfeld.Chronicles.1080p.mkv", 1320, []episodeid.ID{{Season: 1, Episode: 1}}, false},
		{"Seinfeld - 9x12 - The Reverse Peephole.mp4", 1320, []episodeid.ID{{Season: 9, Episode: 12}}, false},
		{"Season 09/12 - The Reverse Peephole.mkv", 1320, []episodeid.ID{{Season: 9, Episode: 12}}, false},
		{"The_Reverse_Peepole_1080p.mkv", 1320, []episodeid.ID{{Season: 9, Episode: 12}}, false},
		{"The Soup Nazi.mkv", 1320, []episodeid.ID{{Season: 7, Episode: 6}}, false},
		{"The Seinfeld Chronicles.mkv", 1320, []episodeid.ID{{Season: 1, Episode: 1}}, false},
		{"The Boyfriend Part 2.mkv", 1320, []episodeid.ID{{Season: 3, Episode: 18}}, false},
		{"The Boyfriend.mkv", 2640, []episodeid.ID{{Season: 3, Episode: 17}, {Season: 3, Episode: 18}}, false},
		{"S03E17E18 - The Boyfriend.mkv", 2640, []episodeid.ID{{Season: 3, Episode: 17}, {Season: 3, Episode: 18}}, false},
		{"The Boyfriend.mkv", 1320, nil, true},
		{"S03E17E18 - The Boyfriend.mkv", 1320, nil, true},
		{"S01E01E02 - The Seinfeld Chronicles.mkv", 2640, nil, true},
		{"S01E02 - Male Unbonding.mkv", 1320, nil, true},
		{"S09E99.mkv", 1320, nil, true},
		{"random 1080p 1994.mkv", 1320, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := c.Match(tc.name, tc.seconds)
			if tc.bad {
				if err == nil {
					t.Fatalf("unexpected match %v", got)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("match=%v error=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestEveryCatalogEpisodeRoundTripsThroughCanonicalFilename(t *testing.T) {
	c := testCatalog(t)
	for _, episode := range c.Episodes {
		ids := []episodeid.ID{episode.ID}
		name := c.filename(ids, ".mkv")
		got, _, err := c.Match(name, 1320)
		if err != nil || !reflect.DeepEqual(got, ids) {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
}

func fixture(t *testing.T) *Importer {
	t.Helper()
	src, dst, state := t.TempDir(), t.TempDir(), t.TempDir()
	i, err := New(testCatalog(t), src, dst, filepath.Join(state, "import-status.json"), func(message string) { t.Log(message) })
	if err != nil {
		t.Fatal(err)
	}
	i.probe = func(context.Context, string) (float64, error) { return 1320, nil }
	i.free = func(string) (uint64, error) { return 100 * 1024 * 1024 * 1024, nil }
	return i
}

func writeVideo(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, i *Importer) {
	t.Helper()
	files, _, err := i.sources()
	if err != nil {
		t.Fatal(err)
	}
	i.runBatch(context.Background(), files)
}

func TestCopyDeduplicateRetryAndPersistentChecklist(t *testing.T) {
	i := fixture(t)
	source := writeVideo(t, i.source, "Seinfeld/Season 1/Seinfeld - 1x01.mkv", "source bytes unchanged")
	writeVideo(t, i.source, "duplicate s01e01.mp4", "second encoding should not be imported")
	writeVideo(t, i.source, "unknown.mkv", "unknown identity")
	writeVideo(t, i.source, "commercials/s01e02.mkv", "not an episode")
	run(t, i)
	s := i.Snapshot()
	if s.Present != 1 || s.Missing != 179 || !s.SafeToRemove || s.Phase != "attention" {
		t.Fatalf("status %+v", s)
	}
	if len(s.Results) != 3 {
		t.Fatalf("results=%v", s.Results)
	}
	bytes, err := os.ReadFile(filepath.Join(i.destination, "s01e01_the_seinfeld_chronicles.mkv"))
	if err != nil || string(bytes) != "source bytes unchanged" {
		t.Fatalf("destination %q %v", bytes, err)
	}
	bytes, err = os.ReadFile(source)
	if err != nil || string(bytes) != "source bytes unchanged" {
		t.Fatal("source changed")
	}
	staging, _ := filepath.Glob(filepath.Join(i.destination, ".import-*"))
	if len(staging) != 0 {
		t.Fatal("staging leak", staging)
	}
	// Reinsert/retry never duplicates or overwrites a completed episode.
	run(t, i)
	dest, _ := os.ReadDir(i.destination)
	if len(dest) != 1 {
		t.Fatalf("files on SD=%d", len(dest))
	}
	report, err := os.ReadFile(i.report)
	if err != nil {
		t.Fatal(err)
	}
	var saved Status
	if err := json.Unmarshal(report, &saved); err != nil || saved.Present != 1 {
		t.Fatalf("saved report %v %v", saved, err)
	}
	// Completion is reconstructed from SD, including a manually repaired file.
	writeVideo(t, i.destination, "s09e12_the_reverse_peephole.mkv", "manual copy")
	i.refreshLocal(context.Background())
	if got := i.Snapshot().Present; got != 2 {
		t.Fatalf("manual repair not found: %d", got)
	}
}

func TestDiskFullCorruptAndCancelledFilesAreNeverPublished(t *testing.T) {
	i := fixture(t)
	src := writeVideo(t, i.source, "s01e01.mkv", "real transfer bytes")
	i.free = func(string) (uint64, error) { return reserveBytes, nil }
	run(t, i)
	s := i.Snapshot()
	if s.Present != 0 || len(s.Results) != 1 || s.Results[0].Status != "failed" || !strings.Contains(s.Results[0].Message, "not enough SD space") {
		t.Fatalf("disk full status %+v", s)
	}
	entries, _ := os.ReadDir(i.destination)
	if len(entries) != 0 {
		t.Fatal("full disk published file")
	}
	i.free = func(string) (uint64, error) { return 100 * 1024 * 1024 * 1024, nil }
	i.probe = func(context.Context, string) (float64, error) { return 0, errors.New("unreadable video") }
	i.validated = map[string]validation{}
	run(t, i)
	if i.Snapshot().Present != 0 {
		t.Fatal("corrupt file counted")
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := i.copy(ctx, sourceFile{src, "s01e01.mkv", info}, filepath.Join(i.destination, "test.mkv")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	entries, _ = os.ReadDir(i.destination)
	if len(entries) != 0 {
		t.Fatal("cancelled file visible or staging leaked", entries)
	}
}

func TestAtomicPublicationDoesNotOverwriteManualFile(t *testing.T) {
	i := fixture(t)
	src := writeVideo(t, i.source, "s01e01.mkv", "new bytes")
	dest := writeVideo(t, i.destination, "same.mkv", "keep these original bytes")
	info, _ := os.Stat(src)
	if err := i.copy(context.Background(), sourceFile{src, "s01e01.mkv", info}, dest); err == nil {
		t.Fatal("overwrote existing file")
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "keep these original bytes" {
		t.Fatal("existing file changed")
	}
}

func TestCombinedFileCoversBothPartsOnlyOnce(t *testing.T) {
	i := fixture(t)
	i.probe = func(context.Context, string) (float64, error) { return 2640, nil }
	writeVideo(t, i.source, "The Boyfriend.mkv", "two part video")
	run(t, i)
	s := i.Snapshot()
	if s.Present != 2 {
		t.Fatalf("present=%d results=%v", s.Present, s.Results)
	}
	entries, _ := os.ReadDir(i.destination)
	if len(entries) != 1 || entries[0].Name() != "s03e17e18_the_boyfriend.mkv" {
		t.Fatalf("files=%v", entries)
	}
}

func TestRejectNestedRootsAndSkipSourceSymlinks(t *testing.T) {
	i := fixture(t)
	if _, err := New(i.catalog, i.source, filepath.Join(i.source, "local"), "", nil); err == nil {
		t.Fatal("accepted overlapping roots")
	}
	external := writeVideo(t, t.TempDir(), "s01e01.mkv", "outside USB")
	if err := os.Symlink(external, filepath.Join(i.source, "s01e01.mkv")); err != nil {
		t.Fatal(err)
	}
	files, _, err := i.sources()
	if err != nil || len(files) != 0 {
		t.Fatalf("followed source symlink: %v %v", files, err)
	}
}

func TestRealFFprobeAndByteVerifiedCopy(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	i := fixture(t)
	i.probe = probeVideo
	source := filepath.Join(i.source, "Seinfeld S09E12 The Reverse Peephole.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x64:d=1", "-c:v", "mpeg4", source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	run(t, i)
	if s := i.Snapshot(); s.Present != 1 {
		t.Fatalf("real video not imported: %+v", s.Results)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("USB original missing")
	}
}

func TestRetryRevalidatesSameSizeAndTimestampSource(t *testing.T) {
	i := fixture(t)
	src := writeVideo(t, i.source, "s01e01.mkv", "bad!")
	original, _ := os.Stat(src)
	i.probe = func(_ context.Context, path string) (float64, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		if string(data) == "bad!" {
			return 0, errors.New("damaged source")
		}
		return 1320, nil
	}
	run(t, i)
	if i.Snapshot().Present != 0 {
		t.Fatal("invalid source imported")
	}
	if err := os.WriteFile(src, []byte("good"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(src, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	run(t, i)
	if s := i.Snapshot(); s.Present != 1 {
		t.Fatalf("retry reused stale validation: %+v", s.Results)
	}
}

func TestUSBIdentityChangesEvenWithIdenticalFileMetadata(t *testing.T) {
	i := fixture(t)
	first := writeVideo(t, i.source, "s01e01.mkv", "same")
	info, _ := os.Stat(first)
	_, before, err := i.sources()
	if err != nil {
		t.Fatal(err)
	}
	i.source = t.TempDir()
	second := writeVideo(t, i.source, "s01e01.mkv", "same")
	if err := os.Chtimes(second, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	_, after, err := i.sources()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("new source identity was ignored")
	}
}
