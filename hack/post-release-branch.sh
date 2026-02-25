#!/bin/bash

# Script: post-release-branch.sh
# Purpose: Triggered on every semantic release to manage version branches
#
# On Minor/Major releases (patch version = 0):
#   1. Create/update stable branch from the released version
#   2. Create maintenance branch (release/vM.m) from the previous release
#
# Usage: post-release-branch.sh <version>
# Example: post-release-branch.sh v1.4.0

set -e

readonly basename="${0##*/}"
readonly scriptname="${basename%.*}"

# ============================================================================
# Utility Functions
# ============================================================================

msg() {
  echo >&2 "[$(date +'%-I:%M:%S %p')] [$scriptname] $*"
}

error() {
  msg "Error: $*"
  exit 1
}

# ============================================================================
# Input Validation
# ============================================================================

RELEASED_VERSION="${1:-}"

if [ -z "$RELEASED_VERSION" ]; then
  error "Missing arguments. Usage: ${basename} <released_version>"
fi

# Normalize version (remove 'v' prefix for processing, add it back for git refs)
CLEAN_VERSION="${RELEASED_VERSION#v}"
RELEASED_VERSION_TAG="v${CLEAN_VERSION}"

# Extract version components
PATCH=$(echo "$CLEAN_VERSION" | cut -d. -f3)

# ============================================================================
# Git Setup
# ============================================================================

msg "Fetching tags from remote..."
git fetch --tags --quiet

# ============================================================================
# Create Stable Branch - For all minor/major releases
# ============================================================================

create_stable_branch() {
  local version=$1
  local stable_branch="stable"

  msg "Creating stable branch: $stable_branch from $version"

  git branch "$stable_branch" "$version" 2>/dev/null || git branch -f "$stable_branch" "$version"
  git push origin "$stable_branch" --force

  msg "Success: Stable branch '$stable_branch' updated."
}

# ============================================================================
# Create Maintenance Branch (release/vM.m) - For minor/major releases only
# ============================================================================

create_maintenance_branch() {
  local current_tag=$1
  local maintenance_branch
  local previous_tag

  # Find the most recent tag older than the current release
  previous_tag=$( (git tag -l 'v*'; echo "$current_tag") | sort -V | uniq | grep -B 1 -x "$current_tag" | head -n 1)

  # Check if we found a previous tag to base maintenance branch on
  if [ "$previous_tag" == "$current_tag" ]; then
    msg "Info: No previous release found. Cannot create maintenance branch, skipping..."
    return 0
  fi

  # Extract Major/Minor from the previous tag (e.g., v1.3.4 -> v1.3)
  local clean_tag="${previous_tag#v}"
  local tag_major
  local tag_minor

  tag_major=$(echo "$clean_tag" | cut -d. -f1)
  tag_minor=$(echo "$clean_tag" | cut -d. -f2)
  maintenance_branch="release/v${tag_major}.${tag_minor}"

  msg "Creating maintenance branch: $maintenance_branch from $previous_tag"

  # Idempotency check: Does the branch already exist?
  if git ls-remote --heads origin "$maintenance_branch" | grep -q "$maintenance_branch"; then
    msg "Info: Maintenance branch $maintenance_branch already exists, skipping..."
    return 0
  fi

  git branch "$maintenance_branch" "$previous_tag"
  git push origin "$maintenance_branch"

  msg "Success: Maintenance branch $maintenance_branch created and pushed."
}

# ============================================================================
# Main Execution
# ============================================================================

# Check if this is a minor/major release (PATCH == 0)
if [ "$PATCH" != "0" ]; then
  msg "Info: Patch release (${RELEASED_VERSION}). Skipping branch operations."
  exit 0
fi

msg "Detected minor/major release: $RELEASED_VERSION"

# Create stable branch (always for minor/major releases)
create_stable_branch "$RELEASED_VERSION_TAG"

# Create maintenance branch from previous release
create_maintenance_branch "$RELEASED_VERSION_TAG"

msg "Release branch operations completed successfully."