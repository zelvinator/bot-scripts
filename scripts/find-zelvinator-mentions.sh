#!/usr/bin/env bash
# find-zelvinator-mentions.sh — Wrapper that calls the zelvinator CLI.
# Falls back to the bash implementation if the Go binary is missing.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Look for the zelvinator CLI
ZELVINATOR_BIN="${SCRIPT_DIR}/zelvinator/zelvinator"
BASH_FALLBACK="${SCRIPT_DIR}/find-zelvinator-mentions.sh.bash"

if [ -x "$ZELVINATOR_BIN" ]; then
  # Support --reset flag
  if [ "${1:-}" = "--reset" ]; then
    exec "$ZELVINATOR_BIN" find --reset
  else
    exec "$ZELVINATOR_BIN" find
  fi
elif [ -f "$BASH_FALLBACK" ]; then
  exec bash "$BASH_FALLBACK" "$@"
else
  echo "Error: No executable found (tried $ZELVINATOR_BIN and $BASH_FALLBACK)" >&2
  echo '[]'
  exit 1
fi
