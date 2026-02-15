#!/bin/bash

set -e

NEXT_VERSION=$1
GIT_TAG=$2

msg () {
  echo >&2 "[$(date +'%-I:%M:%S %p')] [auto-release-branch] $*"
}

if [ -z "$NEXT_VERSION" ] || [ -z "$GIT_TAG" ]; then
  msg "Error: Missing arguments. Usage: ./create-release-branch.sh <version> <tag>"
  exit 1
fi

# Extract components (e.g., 1.2.0 -> MAJOR=1, MINOR=2, PATCH=0)
MAJOR=$(echo "$NEXT_VERSION" | cut -d. -f1)
MINOR=$(echo "$NEXT_VERSION" | cut -d. -f2)
PATCH=$(echo "$NEXT_VERSION" | cut -d. -f3)

BRANCH_NAME="release/v$MAJOR.$MINOR"

msg "Checking release criteria for $NEXT_VERSION ..."

# Logic: Only create branch if it's a new Minor or Major (Patch is 0)
if [ "$PATCH" -eq 0 ]; then
  msg "Verified: This is a new Minor/Major release."

  # Check if the branch already exists on remote
  if git ls-remote --heads origin "$BRANCH_NAME" | grep -q "$BRANCH_NAME"; then
    msg "Result: Branch $BRANCH_NAME already exists on origin. Skipping creation."
  else
    msg "Result: Branch $BRANCH_NAME not found. Creating from $GIT_TAG ..."

    # Create the branch locally at the tag point and push
    git branch "$BRANCH_NAME" "$GIT_TAG"
    git push origin "$BRANCH_NAME"

    msg "Success: $BRANCH_NAME has been pushed to origin."
  fi
else
  msg "Result: Patch version is $PATCH. Skipping branch creation ..."
fi