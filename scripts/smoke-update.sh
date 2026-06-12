#!/usr/bin/env bash
# Smoke test for the OTA update pipeline.
#
# Tests:
#   1. Successful binary swap and cleanup
#   2. Rollback fires when new binary is broken
#   3. Graceful failure when staged file is absent
#
# Usage:
#   sudo ./scripts/smoke-update.sh          # real run on a Pi
#   ./scripts/smoke-update.sh --dry-run     # local dry-run (no systemd)

set -euo pipefail

DRY_RUN=false
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=true

PASS=0; FAIL=0
ok()   { echo "  ✓ $*"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $*"; FAIL=$((FAIL+1)); }
section() { echo; echo "=== $* ==="; }

# --- Scratch directory ---
SCRATCH="$(mktemp -d)"
BINARY="$SCRATCH/jerry"
STAGED="$SCRATCH/jerry.staged"
BACKUP="$SCRATCH/jerry.prev"
PENDING="$SCRATCH/update.pending"
SOCKET="$SCRATCH/mpv-jerry.sock"
LOG="$SCRATCH/update.log"

cleanup() { rm -rf "$SCRATCH"; }
trap cleanup EXIT

# Patch the apply script for this scratch environment.
APPLY_SRC="$(cd "$(dirname "$0")/.." && pwd)/deploy/update-apply.sh"
APPLY="$SCRATCH/update-apply.sh"
sed \
  -e "s|INSTALL_DIR=\"/opt/jerry\"|INSTALL_DIR=\"$SCRATCH\"|" \
  -e "s|SOCKET=\"/tmp/mpv-jerry.sock\"|SOCKET=\"$SOCKET\"|" \
  -e "s|LOG_FILE=\"/var/log/jerry-update.log\"|LOG_FILE=\"$LOG\"|" \
  -e "s|STARTUP_TIMEOUT=30|STARTUP_TIMEOUT=5|" \
  "$APPLY_SRC" > "$APPLY"
chmod +x "$APPLY"

# In dry-run mode stub out systemctl so it creates the socket
# (simulating the new binary coming up) — uses a real Unix socket.
run_apply() {
  if $DRY_RUN; then
    # Temporarily prepend a stub systemctl that creates the socket.
    STUB_DIR="$SCRATCH/stubs"
    mkdir -p "$STUB_DIR"
    # Python one-liner creates a real Unix domain socket file.
    printf '#!/usr/bin/env bash\necho "[stub] systemctl $*"\npython3 -c "import socket as S,sys; s=S.socket(S.AF_UNIX); s.bind(sys.argv[1]); s.listen(1)" "%s" &\n' "$SOCKET" > "$STUB_DIR/systemctl"
    chmod +x "$STUB_DIR/stubs" 2>/dev/null || true
    chmod +x "$STUB_DIR/systemctl"
    PATH="$STUB_DIR:$PATH" bash "$APPLY" "$@"
  else
    sudo "$APPLY" "$@"
  fi
}

run_apply_broken() {
  # Broken variant: systemctl stub does NOT create the socket so health check times out.
  if $DRY_RUN; then
    STUB_DIR="$SCRATCH/stubs-broken"
    mkdir -p "$STUB_DIR"
    printf '#!/usr/bin/env bash\necho "[stub] systemctl $*"\n# deliberately do not create socket\n' > "$STUB_DIR/systemctl"
    chmod +x "$STUB_DIR/systemctl"
    # Use a 2s timeout patch so the test doesn't take 30s.
    APPLY_FAST="$SCRATCH/update-apply-fast.sh"
    sed 's|STARTUP_TIMEOUT=5|STARTUP_TIMEOUT=2|' "$APPLY" > "$APPLY_FAST"
    chmod +x "$APPLY_FAST"
    PATH="$STUB_DIR:$PATH" bash "$APPLY_FAST" "$@" || true
  else
    # On real Pi: temporarily rename the staged binary to something that crashes fast.
    sudo "$APPLY" "$@" || true
  fi
}

# ─── Test 1: successful swap ───────────────────────────────────────────────
section "Test 1: successful swap + cleanup"

printf '#!/bin/sh\necho jerry-OLD\n' > "$BINARY";  chmod +x "$BINARY"
printf '#!/bin/sh\necho jerry-NEW\n' > "$STAGED";  chmod +x "$STAGED"
echo "v0.2.0" > "$PENDING"
rm -f "$SOCKET" "$BACKUP"

run_apply v0.2.0
STATUS=$?

[[ $STATUS -eq 0 ]]       && ok "exit 0"                     || fail "exit $STATUS"
[[ ! -f "$STAGED" ]]      && ok "staged file removed"         || fail "staged file still present"
[[ ! -f "$PENDING" ]]     && ok "pending flag cleared"        || fail "pending flag still present"
[[ ! -f "$BACKUP" ]]      && ok "backup cleaned up"           || fail "backup still present"
OUT=$("$BINARY" 2>/dev/null || true)
[[ "$OUT" == *"jerry-NEW"* ]] && ok "new binary is live"      || fail "binary not swapped (got: $OUT)"

# ─── Test 2: rollback on broken binary ─────────────────────────────────────
section "Test 2: rollback when new binary fails to start"

printf '#!/bin/sh\necho jerry-GOOD\n' > "$BINARY";  chmod +x "$BINARY"
printf '#!/bin/sh\nexit 1\n'          > "$STAGED";  chmod +x "$STAGED"
echo "v0.3.0" > "$PENDING"
rm -f "$SOCKET" "$BACKUP"

run_apply_broken v0.3.0

OUT=$("$BINARY" 2>/dev/null || true)
[[ "$OUT" == *"jerry-GOOD"* ]] && ok "rollback restored previous binary" || fail "rollback failed (got: $OUT)"

# ─── Test 3: missing staged file ───────────────────────────────────────────
section "Test 3: fail gracefully when staged file absent"

rm -f "$STAGED"
run_apply_broken v0.4.0
# Should exit non-zero and leave the running binary untouched.
[[ -f "$BINARY" ]] && ok "running binary intact after aborted apply" || fail "binary disappeared"

# ─── Summary ───────────────────────────────────────────────────────────────
echo
echo "Results: $PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]] && exit 0 || exit 1
