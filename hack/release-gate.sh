#!/usr/bin/env bash

# release-gate.sh
# Determines if a Go project release should be triggered based on GitHub events.
# Usage: ./release-gate.sh --event <event> --ref <ref> --ref-name <ref_name> --branch <branch> --message <message>

set -e

# --- Default Values ---
EVENT=""
REF=""
REF_NAME=""
BRANCH=""
MESSAGE=""
RUN_RELEASE=false

# --- Helper: Usage ---
usage() {
  cat <<EOF
Usage: $0 [options]
Options:
  --event STR      GitHub event name (e.g., workflow_run, push, workflow_dispatch)
  --ref STR        Full git ref (e.g., refs/heads/main)
  --ref-name STR   Short branch name (e.g., main)
  --branch STR     Branch name from workflow_run context
  --message STR    Commit message to scan for release markers
EOF
  exit 1
}

msg() {
  echo >&2 "$*"
}

# --- Parse Arguments ---
while [[ $# -gt 0 ]]; do
  case $1 in
    --event)    EVENT="$2"; shift 2 ;;
    --ref)      REF="$2"; shift 2 ;;
    --ref-name) REF_NAME="$2"; shift 2 ;;
    --branch)   BRANCH="$2"; shift 2 ;;
    --message)  MESSAGE="$2"; shift 2 ;;
    *) usage ;;
  esac
done

msg "--- Release Gate Evaluation ---"
msg "Event: $EVENT"

# --- Logic 1: Manual Trigger (workflow_dispatch) ---
if [[ "$EVENT" == "workflow_dispatch" ]]; then
  if [[ "$REF_NAME" == "main" || "$REF_NAME" == release/v* ]]; then
    msg "✅ Match: Manual release authorized on $REF_NAME branch."
    RUN_RELEASE=true
  else
    msg "❌ Block: Manual releases are only permitted from the 'main' or maintenance (release/v*) branch."
  fi

# --- Logic 2: Automated RC (workflow_run from main) ---
elif [[ "$EVENT" == "workflow_run" ]]; then
  if [[ "$BRANCH" == "main" ]]; then
    # Scan for Release-Channel or Release-As markers
    if echo "$MESSAGE" | grep -Ei "(Release-As|Release-Channel):\s*(rc|release-candidate)" > /dev/null; then
      msg "✅ Match: Main branch with valid Release-Channel marker found."
      RUN_RELEASE=true
    else
      msg "ℹ️ Info: No release marker found in commit message on main."
    fi
  else
    msg "ℹ️ Info: workflow_run triggered from outside 'main' or maintenance branch ($BRANCH). Skipping."
  fi

# --- Logic 3: Maintenance Patches (push to release/v*) ---
elif [[ "$EVENT" == "push" ]]; then
  if [[ "$REF" == refs/heads/release/v* ]]; then
    msg "✅ Match: Push to maintenance branch ($REF) detected."
    RUN_RELEASE=true
  elif [[ "$REF" == "refs/heads/stable" ]]; then
    msg "✅ Match: Push to stable branch detected."
    RUN_RELEASE=true
  else
    msg "ℹ️ Info: Push to $REF does not meet release criteria."
  fi

else
  msg "⚠️ Warning: Event type '$EVENT' is not handled by the release gate."
fi

# --- Set GitHub Actions Output ---
msg "--- Result ---"
msg "Final Decision: should_release=$RUN_RELEASE"

# Write to GITHUB_OUTPUT if the environment variable exists
if [[ -n "$GITHUB_OUTPUT" ]]; then
  echo "run=$RUN_RELEASE" >> "$GITHUB_OUTPUT"
fi