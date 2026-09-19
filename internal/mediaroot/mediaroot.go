// Package mediaroot selects the removable media directory used by the
// appliance. A fixed mount point is preferred so the choice is stable across
// boots; when it is absent, common desktop/Linux mount locations are checked.
package mediaroot

import (
	"os"
	"path/filepath"
	"strings"
)

const Auto = "auto"

// Resolve returns an explicit path unchanged. For auto mode it prefers the
// appliance mount point and then finds the first candidate containing either
// jerry.conf or a playable video file.
func Resolve(configured string) string {
	if configured != "" && !strings.EqualFold(configured, Auto) {
		return configured
	}

	if isDir("/media/usb") {
		return "/media/usb"
	}
	for _, parent := range []string{"/media", "/mnt", "/run/media"} {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "usb" {
				continue
			}
			path := filepath.Join(parent, entry.Name())
			if hasMedia(path) {
				return path
			}
		}
	}
	// Keep a deterministic path while no drive is present. The watcher can
	// discover a drive later, and the firstboot layout creates this directory.
	return "/media/usb"
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func hasMedia(root string) bool {
	if _, err := os.Stat(filepath.Join(root, "jerry.conf")); err == nil {
		return true
	}
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".mp4", ".mkv", ".mov", ".avi", ".m4v", ".webm":
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
