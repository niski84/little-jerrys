# TMDB cache (build-time)

This directory is populated by the `--prefetch-tmdb` CLI:

```sh
JERRY_TMDB_API_KEY=your-key ./little-jerrys --prefetch-tmdb
```

The contents (show.json, seasons/NN/season.json, seasons/NN/episodes/NN.jpg)
are baked into the Go binary via `go:embed` on the next build, so the
appliance reads metadata + stills with zero network calls at runtime.

This file exists so the embed always has at least one entry. Don't delete it.
