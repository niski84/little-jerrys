// Package clipart catalogs the screensaver images bundled in the binary.
// Source files live at web/jerry/static/img/clipart/ and are embedded by
// web/embed.go. This package walks that subtree and returns the URL paths
// the slideshow page consumes via /api/slideshow/catalog.
//
// The slideshow itself runs in the browser — the Pi launches chromium kiosk
// on HDMI-1 pointing at /slideshow. The browser reads this catalog, does a
// Fisher-Yates shuffle, and walks through (refreshing on exhaustion). That
// gives the "true random — every image plays before any repeats" property
// the user asked for.
package clipart

import (
	"io/fs"
	"path"
	"strings"
)

// Extensions the slideshow recognizes.
var Extensions = []string{".jpg", ".jpeg", ".png", ".webp", ".avif", ".gif", ".bmp"}

// Scan walks the embedded clipart subtree and returns URL paths under
// /static/img/clipart/. Caller hands these to the browser as <img src>.
func Scan(staticFS fs.FS) ([]string, error) {
	var out []string
	err := fs.WalkDir(staticFS, "img/clipart", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(path.Ext(p))
		for _, allowed := range Extensions {
			if ext == allowed {
				out = append(out, "/static/"+p)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		// Subtree missing entirely — return empty rather than error so the
		// slideshow page degrades to "0 images" instead of 500.
		return nil, nil
	}
	return out, nil
}
