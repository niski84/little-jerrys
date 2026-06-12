#!/usr/bin/env bash
# Cross-compile the Little Jerry's binary for Raspberry Pi (arm64).
#
# Output: ./build/jerry-arm64
#
# Builds with `-tags pi` so the GPIO bailout-button package compiles in the
# real periph.io implementation instead of the dev stub. Statically linked
# (CGO disabled) so we don't have to worry about glibc versions on the Pi.
#
# Run on the dev workstation, not on the Pi itself.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

mkdir -p build

echo "→ templ generate…"
go run github.com/a-h/templ/cmd/templ@latest generate -path ./internal/jerry/views

if [[ -f package.json ]]; then
  if [[ ! -d node_modules ]]; then
    echo "→ npm install…"
    npm install --silent --no-audit --no-fund
  fi
  echo "→ Tailwind (static CSS, minified)…"
  npm run --silent build:css
fi

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo v0.0.0-dev)}"
echo "→ go build linux/arm64 -tags pi (version=$VERSION)…"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -tags pi -trimpath \
  -ldflags="-s -w -X main.Version=${VERSION}" \
  -o build/jerry-arm64 ./cmd/little-jerrys

ls -lh build/jerry-arm64
file build/jerry-arm64
echo "✓ Pi binary at $PROJECT_DIR/build/jerry-arm64"
