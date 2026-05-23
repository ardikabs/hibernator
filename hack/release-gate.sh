#!/usr/bin/env bash

set -e

# --- Default Values ---
EVENT=""
BRANCH_INPUT=""
MESSAGE=""
RUN_RELEASE=false

usage() {
  cat <<EOF
Usage: $0 --event <event> --branch <branch_or_ref> --message <message>
EOF
  exit 1
}

msg() { echo >&2 "$*"; }

# --- Parse Arguments ---
while [[ $# -gt 0 ]]; do
  case $1 in
    --event)   EVENT="$2"; shift 2 ;;
    --branch)  BRANCH_INPUT="$2"; shift 2 ;;
    --message) MESSAGE="$2"; shift 2 ;;
    *) usage ;;
  esac
done

# --- Normalization ---
# Strip 'refs/heads/' prefix if present to get the clean branch name
# (e.g., 'refs/heads/main' -> 'main')
TARGET_BRANCH="${BRANCH_INPUT#refs/heads/}"

msg "--- Release Gate Evaluation ---"
msg "Event:  $EVENT"
msg "Branch: $TARGET_BRANCH"

# --- Evaluation Logic ---

case "$EVENT" in
  "workflow_dispatch")
    # Manual Trigger: Authorization check
    if [[ "$TARGET_BRANCH" == "main" || "$TARGET_BRANCH" == release/v* ]]; then
      msg "✅ Match: Release authorized for cutoff branch ($TARGET_BRANCH)."
      RUN_RELEASE=true
    else
      msg "❌ Block: $TARGET_BRANCH is not a valid release path."
      msg "--- Available Release Paths ---"
      msg "1. Release: Manual trigger on a cutoff branch (main)."
      msg "2. Release Candidate: Automated flow on 'main' via commit markers (e.g., Release-As: rc)."
    fi
    ;;

  "workflow_run")
    # Automated RC: Only from main + commit message marker
    if [[ "$TARGET_BRANCH" == "main" ]]; then
      if echo "$MESSAGE" | grep -Ei "(Release-As|Release-Channel):\s*(rc|release-candidate)" > /dev/null; then
        msg "✅ Match: Release marker found on main branch."
        RUN_RELEASE=true
      else
        msg "ℹ️ Info: No release marker in message."
      fi
    else
      msg "ℹ️ Info: workflow_run on $TARGET_BRANCH ignored."
    fi
    ;;

  "push")
    # Maintenance/Stable: Automated on push
    if [[ "$TARGET_BRANCH" == release/v* ]]; then
      msg "✅ Match: Maintenance branch push event detected ($TARGET_BRANCH)."
      RUN_RELEASE=true
    else
      msg "ℹ️ Info: Push to $TARGET_BRANCH does not trigger release."
    fi
    ;;

  *)
    msg "⚠️ Warning: Event '$EVENT' unhandled."
    ;;
esac

# --- Result ---
msg "Final Decision: should_release=$RUN_RELEASE"

if [[ -n "$GITHUB_OUTPUT" ]]; then
  echo "run=$RUN_RELEASE" >> "$GITHUB_OUTPUT"
fi