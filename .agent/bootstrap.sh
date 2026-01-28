#!/usr/bin/env bash
# Minimal agent bootstrap helper
# Purpose: locate and print AGENTS.md so any automated agent can source guidance before work

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AGENTS_FILE="$REPO_ROOT/AGENTS.md"

if [ ! -f "$AGENTS_FILE" ]; then
  echo "AGENTS.md not found in $REPO_ROOT"
  exit 1
fi

echo "--- AGENTS.md (start) ---"
cat "$AGENTS_FILE"
echo "--- AGENTS.md (end) ---"

exit 0
