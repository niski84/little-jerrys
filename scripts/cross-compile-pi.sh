#!/usr/bin/env bash
# Rebuild the production ARM64 Little Jerry's binary from the locked project inputs.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

mkdir -p build

TEMPL_VERSION=$(go list -m -f '{{.Version}}' github.com/a-h/templ)
[[ -n "$TEMPL_VERSION" ]] || { echo "error: cannot resolve pinned templ version" >&2; exit 1; }
echo "→ templ generate (v$TEMPL_VERSION)…"
go run "github.com/a-h/templ/cmd/templ@${TEMPL_VERSION}" generate -path ./internal/jerry/views

if [[ -f package.json ]]; then
  [[ -f package-lock.json ]] || { echo "error: package-lock.json is required for release builds" >&2; exit 1; }
  echo "→ npm ci…"
  npm ci --silent --no-audit --no-fund
  echo "→ Tailwind (static CSS, minified)…"
  npm run --silent build:css
fi

VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo v0.0.0)}"
echo "→ rebuilding linux/arm64 -tags pi (version=$VERSION)…"
rm -f build/jerry-arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -tags pi -trimpath -buildvcs=true \
  -ldflags="-s -w -X main.Version=${VERSION}" \
  -o build/jerry-arm64 ./cmd/little-jerrys

[[ -s build/jerry-arm64 ]] || { echo "error: cross-compile produced no binary" >&2; exit 1; }
file build/jerry-arm64
echo "✓ rebuilt $PROJECT_DIR/build/jerry-arm64"
