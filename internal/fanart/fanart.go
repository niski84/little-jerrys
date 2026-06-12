// Package fanart scans the USB drive for images shown during off-hours.
// Source images live under <media-root>/fan-art/ and are displayed by mpv
// as a slideshow. The scan is best-effort — a missing directory just yields
// an empty list, no error to the caller (off-hours mode degrades to "blank
// TV with a vignette" rather than crashing).
package fanart

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Extensions are the image extensions we recognize.
var Extensions = []string{".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif"}

// Scan returns all image files under <root>/fan-art/, shuffled. Returns
// an empty slice (not an error) when the directory is missing.
func Scan(mediaRoot string) []string {
	dir := filepath.Join(mediaRoot, "fan-art")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	var paths []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		for _, e := range Extensions {
			if ext == e {
				paths = append(paths, path)
				return nil
			}
		}
		return nil
	})
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	rng.Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
	return paths
}
