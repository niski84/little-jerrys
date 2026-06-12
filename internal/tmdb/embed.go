package tmdb

import "embed"

// EmbeddedFS is the build-time-baked TMDB cache. The directory it embeds
// (`internal/tmdb/data/`) lives next to this file by Go's embed-path rules.
// Populated by running `./little-jerrys --prefetch-tmdb` once and
// rebuilding so go:embed picks up the new files.
//
//go:embed all:data
var EmbeddedFS embed.FS
