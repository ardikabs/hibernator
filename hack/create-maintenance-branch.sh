#!/bin/bash

set -e

basename="${0##*/}"
scriptname="${basename%.*}"

# The version we are moving TO (e.g., 1.4.0)
INPUT_VERSION=$1

msg () {
  echo >&2 "[$(date +'%-I:%M:%S %p')] [$scriptname] $*"
}

if [ -z "$INPUT_VERSION" ]; then
  msg "Error: Missing arguments. Usage: ${basename} <next_version>"
  exit 1
fi

# Ensure version doesn't have a 'v' for internal logic, then add it for tag comparison
CLEAN_INPUT="${INPUT_VERSION#v}"
V_INPUT="v$CLEAN_INPUT"

msg "Fetching tags from remote..."
git fetch --tags --quiet >/dev/null

# Find the most recent tag (e.g., v1.3.3)
LATEST_TAG=$( (git tag -l 'v*'; echo "$V_INPUT") | sort -V | uniq | grep -B 1 -x "$V_INPUT" | head -n 1)

if [ "$LATEST_TAG" == "$V_INPUT" ]; then
  msg "Info: No existing tag found older than $V_INPUT. Cannot create maintenance branch, skipping ..."
  exit 0
fi

# Extract Major/Minor from the found tag (e.g., v1.3.3 -> v1.3)
CLEAN_TAG="${LATEST_TAG#v}"
TAG_MAJOR=$(echo "$CLEAN_TAG" | cut -d. -f1)
TAG_MINOR=$(echo "$CLEAN_TAG" | cut -d. -f2)
BRANCH_NAME="release/v$TAG_MAJOR.$TAG_MINOR"

msg "Action: Create $BRANCH_NAME from $LATEST_TAG (Previous to $V_INPUT)"

# Final Idempotency Check: Does the branch already exist on origin?
if git ls-remote --heads origin "$BRANCH_NAME" | grep -q "$BRANCH_NAME"; then
  msg "Result: Branch $BRANCH_NAME already exists, skipping ..."
else
  git branch "$BRANCH_NAME" "$LATEST_TAG"
  git push origin "$BRANCH_NAME"
  msg "Success: $BRANCH_NAME created and pushed."
fi