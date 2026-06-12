# Feature Plan: Venue Branding Pack + Season-Aware Weighting

_Last updated: 2026-05-11 · Status: planning_

---

## Overview

Two related features that give operators meaningful control over what the
appliance shows and how it behaves — without touching Go code.

| Feature | Scope | Impact |
|---|---|---|
| **Venue Branding Pack** | OSD logo overlay, themed UI colors, extended splash, admin preview UI | High visual impact; key Smash Deck differentiator |
| **Season-Aware Weighting** | Season-level multipliers and blacklist; composites with per-episode weights | Fills the gap between "block one episode" and "skip a whole era" |

---

## Feature 1: Venue Branding Pack

### Goal

An operator can drop files on the USB drive and edit `jerry.conf` to get
their venue logo on the TV overlay, their brand colors throughout the web
UI, and a custom splash screen — all without recompiling or SSHing in.

### Current state (what already exists)

| Capability | Location |
|---|---|
| `TENANT_NAME` + `LOGO_FILE` read from `jerry.conf` | `internal/usbconfig/config.go:32-72` |
| `BrandTenantName` / `BrandLogoURL` globals | `internal/jerry/views/branding.go:10-16` |
| `/branding/{path}` HTTP route (serves `/media/usb/branding/`) | `internal/jerry/http.go:74-82` |
| `/api/splash.png` (hardcoded colors, no logo) | `internal/splash/splash.go:37-104` |
| CSS custom properties `--color-bc-royal`, `--color-bc-yellow` | `web/styles/input.css:15-25` |
| Theme toggle (dark/light via `html.dark` class) | `internal/jerry/views/layout.templ:28-38` |

### What we're adding

#### 1.1 Extended `jerry.conf` fields

Six new optional keys. All have sensible defaults so existing deployments
change nothing.

```
# --- Branding colors (hex, with or without #) ---
BRAND_PRIMARY=#FFD700        # Accent/highlight color (default: broadcast amber)
BRAND_ACCENT=#002366         # Surface/card color (default: royal blue)
BRAND_BG=#08080C             # Dark background (used in splash + UI dark mode)

# --- Splash screen copy ---
BRAND_SPLASH_TITLE=          # Overrides venue name on splash (e.g. "Channel 14 | Tony's")
BRAND_SPLASH_SUBTITLE=       # Tagline under title (default: "First-time setup")

# --- OSD logo overlay ---
OSD_LOGO_ENABLED=false       # true to show logo on top of video
OSD_LOGO_POSITION=top-right  # top-left | top-right | bottom-left | bottom-right
OSD_LOGO_OPACITY=80          # 0–100 (applied as PNG alpha pre-multiply)
OSD_LOGO_SCALE=12            # Logo width as % of frame width (default 12)
```

**Implementation:** extend `internal/usbconfig/config.go` with a new
`Branding` sub-struct. Parsed values flow into `views.BrandConfig` at boot
in `app.go:86-96`.

#### 1.2 Runtime CSS variable injection

Rather than recompiling Tailwind (impossible at runtime), override the CSS
custom properties via an injected `<style>` block in `layout.templ`.

```html
<!-- injected once per page render, values come from BrandConfig -->
<style>
  :root {
    --color-bc-yellow: {{ BrandConfig.Primary }};
    --color-bc-royal:  {{ BrandConfig.Accent }};
  }
  html.dark body { background-color: {{ BrandConfig.BgDark }}; }
</style>
```

All existing DaisyUI + custom component styles that reference `--color-bc-*`
inherit the operator's palette automatically. No Tailwind recompile, no
restart required (change `jerry.conf`, reload).

**New globals in `views/branding.go`:**

```go
var BrandConfig = BrandingConfig{
    Primary:  "#FFD700",
    Accent:   "#002366",
    BgDark:   "#08080C",
    SplashTitle:    "",
    SplashSubtitle: "",
}
```

#### 1.3 Splash screen enhancements

`internal/splash/splash.go` currently has hardcoded colors and no logo.

**Changes to `splash.Config` struct:**

```go
type Config struct {
    SSID      string
    AdminURL  string
    Width     int
    Height    int

    // New fields (filled from BrandConfig at call site)
    Primary   color.RGBA   // replaces hardcoded #FFD700
    BgColor   color.RGBA   // replaces hardcoded #08080C
    Title     string       // replaces hardcoded "Little Jerry's"
    Subtitle  string       // replaces hardcoded "First-time setup"
    LogoPath  string       // absolute path to logo file on USB, empty = no logo
}
```

**Logo compositing in `Render()`:**

1. Load and decode the logo PNG from `cfg.LogoPath`.
2. Scale it to fit within 320×120 px (maintaining aspect ratio) using
   `golang.org/x/image/draw` with `draw.BiLinear`.
3. Draw it in the top-left of the splash (beside the title), or centered
   below the title if it's portrait.
4. Fallback silently if the file is missing or unreadable.

No new dependencies — `golang.org/x/image` is already in `go.sum`.

#### 1.4 OSD logo overlay on video

mpv's IPC supports `overlay-add` which draws a raw BGRA image at an
arbitrary pixel position over the current video frame. This is the right
primitive.

**New method on `*Player`:**

```go
// ShowLogoOverlay loads a PNG from disk, converts it to raw BGRA, writes
// it to a temp file, and tells mpv to draw it at the configured corner.
func (p *Player) ShowLogoOverlay(cfg LogoOverlayConfig) error

// HideLogoOverlay removes overlay id 1.
func (p *Player) HideLogoOverlay()

type LogoOverlayConfig struct {
    ImagePath string   // absolute path to PNG on USB
    Position  string   // "top-left" | "top-right" | "bottom-left" | "bottom-right"
    ScalePct  int      // logo width as % of video width (e.g. 12 = 12%)
    Opacity   int      // 0–100
}
```

**IPC command sequence:**

```json
{ "command": ["overlay-add", 1, <x>, <y>, "<rawfile>", 0, "bgra", <w>, <h>, <stride>] }
```

The raw file is written to `/tmp/jerry-logo.bgra` (persistent temp,
overwritten on each call). mpv reads it directly via mmap.

**Call sites:**

- `app.go` after `player.Play()` is first called (once per boot)
- `app.go` inside `OnEndFile` callback (re-applied after each track, since
  mpv resets overlays on `loadfile`)

**Overlay must be re-sent after every `loadfile` command** — this is an
mpv constraint. The player package will expose `ShowLogoOverlay` as a
configurable post-play hook so `app.go` can wire it once.

**Opacity:** Pre-multiply the PNG alpha channel by `cfg.Opacity / 100`
before writing BGRA. mpv's `overlay-add` treats the A channel as
straight alpha.

**Position math (example for 1920×1080 at 12% scale):**

```
logo_w  = 1920 * 0.12 = 230 px
logo_h  = logo_w / aspect_ratio
margin  = 40 px

top-left:      x=40,            y=40
top-right:     x=1920-230-40,   y=40
bottom-left:   x=40,            y=1080-logo_h-40
bottom-right:  x=1920-230-40,   y=1080-logo_h-40
```

For now assume 1920×1080. Future: query mpv `video-params/w` and
`video-params/h` via `get_property` before computing position.

#### 1.5 Admin branding preview UI

New page: `GET /settings/branding`

**Content:**

- Current values table: tenant name, logo file, primary color, accent color,
  OSD overlay enabled/position/opacity.
- Live splash preview: `<img src="/api/splash.png" class="w-full rounded">` —
  refreshes on page load, showing the actual generated PNG.
- Live OSD preview mockup: CSS div that simulates the logo corner overlay
  using the uploaded logo and position values — no mpv required.
- Link to `jerry.conf` docs for editing instructions.
- "Reload branding" button: `POST /api/reload-branding` that re-reads
  `jerry.conf` and `usbconfig` without a full restart.

**No file upload in v1** — operator copies files to USB manually.
This is acceptable; upload can be a future tier feature.

**Route:** Add `GET /settings/branding` and `POST /api/reload-branding` to
`http.go`. Protect both with existing session auth middleware.

**New Templ template:** `internal/jerry/views/settings_branding.templ`

---

### Implementation order

```
1. usbconfig: parse new BRAND_* / OSD_* keys → BrandingConfig struct
2. views/branding.go: add BrandConfig global, populate at boot in app.go
3. layout.templ: inject <style> block with CSS variable overrides
4. splash/splash.go: extend Config, add logo compositing, use brand colors
5. player: add ShowLogoOverlay / HideLogoOverlay, wire BGRA temp file
6. app.go: call ShowLogoOverlay post-play; re-apply in OnEndFile hook
7. http.go + views: /settings/branding page + /api/reload-branding
8. Help doc: add 10-venue-branding.md
```

### Files touched

| File | Change |
|---|---|
| `internal/usbconfig/config.go` | Add `Branding` fields to parsed struct |
| `internal/jerry/views/branding.go` | Add `BrandConfig` + new global vars |
| `internal/jerry/views/layout.templ` | Inject CSS variable override block |
| `internal/jerry/app.go` | Populate BrandConfig at boot; wire OSD hook |
| `internal/splash/splash.go` | Extend Config; add logo compositing |
| `internal/player/player.go` | Add `ShowLogoOverlay`, `HideLogoOverlay` |
| `internal/jerry/http.go` | Add `/settings/branding`, `/api/reload-branding` routes |
| `internal/jerry/views/settings_branding.templ` | New page (new file) |
| `internal/help/docs/10-venue-branding.md` | New help article (new file) |

### Open questions

- **Video resolution:** Should we query mpv for actual video dimensions
  before computing OSD position, or hardcode 1920×1080 for v1?
  _Recommendation: hardcode for v1; add dynamic sizing in a follow-up._
- **Logo format:** PNG only for v1 (simplest decode path). SVG would be
  better for the UI CSS overlay preview. Defer.
- **`reload-branding` scope:** Should it also restart mpv to pick up a new
  OSD overlay, or just update the in-memory config for the next track?
  _Recommendation: update in-memory only; new overlay applies at next
  `loadfile` (within seconds on typical rotation)._

---

## Feature 2: Season-Aware Weighting

### Goal

Operators can bias or completely skip whole seasons in addition to individual
episodes. Useful for: skipping the weak early seasons, heavily featuring
a guest-star era, or blocking finale spoilers at lunch rush.

### Current state

| Capability | Location |
|---|---|
| Per-episode weights (`S01E01` → int) | `internal/jerry/state.go:17` |
| Per-episode blacklist (`[]string`) | `internal/jerry/state.go:16` |
| Weight HTTP endpoint | `internal/jerry/http.go:474-496` |
| Blacklist toggle | `internal/jerry/http.go:497-523` |
| Playlist engine weight application | `internal/playlist/playlist.go:212-221` |
| `ApplyState()` pushes weights on change | `internal/jerry/app.go:591-606` |

### What we're adding

#### 2.1 New state fields

```go
// in internal/jerry/state.go — State struct
SeasonWeights   map[string]int  `json:"season_weights"`    // "S01" → multiplier
SeasonBlacklist []string        `json:"season_blacklist"`  // ["S01", "S09"]
```

Season key format: `"S01"` through `"S09"` (first 3 chars of SNNENN —
trivially extracted with `episodeKey[:3]`).

Default (if absent from JSON): empty map, empty slice — no change to current
behavior. Migration in `state.go:load()` adds nil-coalescing alongside the
existing pattern.

#### 2.2 Composition rules

| Scenario | Effective behavior |
|---|---|
| Season blocked, episode not individually weighted | Episode excluded |
| Season blocked, episode explicitly unblocked (weight ≥ 2) | **Episode still excluded** — season blacklist wins |
| Season weight = 2, episode weight = 3 | Effective pool slots = 2 × 3 = 6 |
| Season weight = 2, episode weight = 0/absent | Effective pool slots = 2 × 1 = 2 |
| Season weight = 0, episode not individually weighted | Episode excluded (equivalent to season blacklist) |
| Season weight = 0, episode weight = 3 | **Episode still excluded** — zero always wins |

**Rule:** Season blacklist and season weight ≤ 0 are both hard excludes.
Per-episode overrides cannot rescue a season-blocked episode. This keeps the
mental model simple: "block the season, done."

If the operator wants one specific episode from a blocked season, they can
move it to a curated playlist instead.

#### 2.3 Playlist engine changes

**`internal/playlist/playlist.go`** — new fields and methods on `Playlist`:

```go
type Playlist struct {
    // existing fields...
    seasonWeights   map[string]int
    seasonBlacklist map[string]bool
}

func (p *Playlist) SetSeasonWeight(season string, weight int)
func (p *Playlist) SetSeasonBlacklist(seasons []string)
```

**`Regenerate()` change** — when building the candidate pool, add a
pre-filter step before checking per-episode blacklist and weights:

```go
// New: season-level pre-filter
season := key[:3]  // "S01E07" → "S01"
if p.seasonBlacklist[season] {
    continue
}
seasonMult := p.seasonWeights[season]
if seasonMult == 0 {
    seasonMult = 1   // absent = no season bias
}
if seasonMult < 0 {
    continue         // treat negative as blocked
}

// Existing per-episode logic:
if p.blacklist[key] { continue }
episodeMult := p.weights[key]
if episodeMult == 0 { episodeMult = 1 }

// Composite:
effectiveMult := seasonMult * episodeMult
for i := 0; i < effectiveMult; i++ {
    pool = append(pool, key)
}
```

#### 2.4 HTTP endpoints

Add to `internal/jerry/http.go`:

```
POST /seasons/{season}/weight   body: weight=<int>   → set season multiplier
POST /seasons/{season}/blocked  body: (none)          → toggle season blacklist
```

Pattern mirrors the existing episode endpoints at lines 474-523. Both call
`store.Update()` then `app.ApplyState()`.

Season path param validation: must match `^S\d{2}$`.

**Extend `ApplyState()` in `app.go`:**

```go
st := store.Get()
// existing episode apply...
pl.SetSeasonBlacklist(st.SeasonBlacklist)
for season, weight := range st.SeasonWeights {
    pl.SetSeasonWeight(season, weight)
}
```

#### 2.5 UI

**Seasons list page** — currently `GET /seasons` shows season cards. Extend
each card with:

- A weight control: slider or `+` / `-` buttons (same pattern as episode
  weight controls on the episode detail card). Values: 0 (skip), 1 (normal,
  default), 2–5 (bias).
- A block toggle: same "No Soup" style toggle. Shows a red badge on the
  season card when active.
- A computed summary line: "9 episodes · **2× weight**" or "**Blocked**".

**Dashboard summary** (existing `/summary` page) — add a "Season weights"
section showing any non-default seasons at a glance.

**Episode detail card** — show effective weight alongside individual weight
when a season multiplier is active:

```
Weight: 3×  (season: 2× × episode: 3× = 6× effective)
```

#### 2.6 Help doc update

Update `internal/help/docs/02-no-soup-blacklist.md` to add a new section:
"Blocking an entire season" with the season-level toggle.

---

### Implementation order

```
1. state.go: add SeasonWeights + SeasonBlacklist; nil-coalesce in load()
2. playlist.go: add season fields + SetSeason* methods; update Regenerate()
3. app.go/ApplyState(): push season weights/blacklist to playlist engine
4. http.go: POST /seasons/{season}/weight + /seasons/{season}/blocked
5. views/seasons.templ: extend season cards with weight + block controls
6. views/episode_detail.templ: show effective weight breakdown when season multiplier ≠ 1
7. views/summary.templ: add "Season weights" section
8. help/docs/02-no-soup-blacklist.md: add season-block section
```

### Files touched

| File | Change |
|---|---|
| `internal/jerry/state.go` | Add `SeasonWeights`, `SeasonBlacklist`; nil-coalesce in `load()` |
| `internal/playlist/playlist.go` | Season fields, `SetSeason*`, update `Regenerate()` |
| `internal/jerry/app.go` | Push season state in `ApplyState()` |
| `internal/jerry/http.go` | Two new POST routes for season weight + block |
| `internal/jerry/views/seasons.templ` | Weight + block controls on season cards |
| `internal/jerry/views/episode_detail.templ` | Effective weight breakdown |
| `internal/jerry/views/summary.templ` | Season weights summary section |
| `internal/help/docs/02-no-soup-blacklist.md` | New "Blocking a season" section |

---

## Phasing suggestion

Both features are independent. Suggested order:

**Phase A (Season-Aware Weighting)** — pure backend + minor UI. Lower risk,
no new dependencies, completely additive. Good to ship first to validate the
composition model before exposing it in branding-adjacent UI.

**Phase B (Venue Branding Pack)** — in two sub-phases:
- **B1:** jerry.conf parsing + CSS variable injection + splash colors/logo.
  No mpv changes. Safe, immediately visible.
- **B2:** OSD logo overlay (mpv `overlay-add`). Isolated to player package.
  Can be feature-flagged with `OSD_LOGO_ENABLED=false` default.

---

## Smash Deck productization note

These two features together define a clear **"Pro venue" tier**:

- Season weighting = curated programming without staff effort
- Venue branding = white-label appliance that looks like it belongs in the space

A future multi-venue dashboard (remote management feature) would expose
both as per-venue config — season weights per location, branding pack per
location. The data model here (USB-local `jerry.conf` + `state.json`)
naturally extends to a cloud-synced config envelope without rearchitecting.
