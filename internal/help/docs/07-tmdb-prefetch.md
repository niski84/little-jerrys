# Episode metadata (build-time)

The browse UI on `/episodes` reads metadata from a cache **baked into the
binary at build time**. Operators of a deployed appliance never see TMDB
or any network round-trip — the data ships embedded.

## Operator note

If you're running a built `.img` or pre-compiled binary, you don't need to
do anything. Episode titles, synopses, air dates, and stills are already
present. Skip the rest of this doc.

The remainder is for **developers building their own image** of the
appliance.

## How the cache is built

The cache lives at `internal/tmdb/data/` and is **not committed to source
control** (it's TMDB-owned content). The build script populates it on each
build:

```sh
JERRY_TMDB_API_KEY=your-key ./binary --prefetch-tmdb
bash scripts/compile.sh
```

This pulls show + 9 seasons + ~180 episode stills from
`api.themoviedb.org`. Total ≈ 10 MB. After this the binary contains the
full cache via `go:embed`. The runtime makes zero TMDB calls.

`.gitignore` already excludes:
- `internal/tmdb/data/show.json`
- `internal/tmdb/data/seasons/`

Only the README stays in version control.

## Getting a TMDB key

https://www.themoviedb.org/settings/api — v3 (32-char hex) and v4 (JWT)
both work. Free for non-commercial usage with the standard attribution
requirement.

## CI / one-shot script

```sh
#!/bin/bash
set -e
test -n "$JERRY_TMDB_API_KEY" || { echo "set JERRY_TMDB_API_KEY"; exit 1; }
go build -o smash-deck ./cmd/little-jerrys
./smash-deck --prefetch-tmdb
bash scripts/compile.sh
echo "binary now has embedded TMDB cache"
```

## Why bake at build time, not runtime?

1. **Offline-first.** The Pi often lives behind restaurant WiFi the
   operator hasn't authorized — never make episode metadata depend on
   networking.
2. **Predictable performance.** No spinner while we wait on TMDB.
3. **Privacy.** The deployed appliance doesn't phone home for routine
   browsing.
4. **Source-control hygiene.** The data is third-party — gitignored, not
   committed.

To refresh metadata (TMDB updated a synopsis or added a better still),
just re-run the prefetch + rebuild on the build machine. Idempotent
skips mean only changed files re-download.

## Episode → file mapping

The Now Playing card resolves a media filename to a TMDB record by
parsing `sNNeNN_*.mp4` patterns (case-insensitive). So
`s02e11_chinese_restaurant.mp4` auto-resolves to `S02E11`. Anything else
shows the bare filename — the browse UI still works either way.
