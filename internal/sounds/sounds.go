// Package sounds catalogs audio clips from a disk directory for the
// soundboard page. The directory lives alongside the other media on the
// USB drive (default: <MediaRoot>/sounds/) — same pattern as episodes
// and commercials. Operator drops files in, hits Rescan on the page, and
// the buttons appear.
package sounds

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Extensions the soundboard recognizes.
var Extensions = []string{".mp3", ".ogg", ".wav", ".m4a"}

// Clip is one button on the soundboard.
type Clip struct {
	URL      string // /sounds/{filename}
	Label    string // humanized from filename (display title)
	Filename string // original filename (shown as caption + matters for GPIO mapping)
	Color    string // optional color hint via "<color>-" prefix (red default)
}

// Scan returns one Clip per audio file in dir, sorted by label. Missing
// directory yields an empty slice — caller renders an empty state.
func Scan(dir string) []Clip {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Clip
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		matched := false
		for _, allowed := range Extensions {
			if ext == allowed {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		color := "red"
		// Recognize "<color>-foo" as a color override (red is default).
		for _, c := range []string{"red", "blue", "yellow", "green", "magenta", "white"} {
			if strings.HasPrefix(strings.ToLower(name), c+"-") {
				color = c
				name = name[len(c)+1:]
				break
			}
		}
		out = append(out, Clip{
			URL:      "/sounds/" + e.Name(),
			Label:    humanize(name),
			Filename: e.Name(),
			Color:    color,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func humanize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
