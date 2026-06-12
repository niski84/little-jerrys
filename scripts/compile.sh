#!/usr/bin/env bash
# templ generate → tailwind build → go build
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

echo "→ templ generate…"
go run github.com/a-h/templ/cmd/templ@latest generate -path ./internal/jerry/views

if [ -f package.json ]; then
  if [ ! -d node_modules ]; then
    echo "→ npm install…"
    npm install --silent --no-audit --no-fund
  fi
  echo "→ Tailwind (static CSS)…"
  npm run --silent build:css
fi

echo "→ go build…"
go build -o little-jerrys ./cmd/little-jerrys
echo "Build OK: $PROJECT_DIR/little-jerrys"
