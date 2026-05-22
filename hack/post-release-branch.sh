#!/bin/bash

# Script: post-release-branch.sh
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
# Input Validation & Parsing
# ============================================================================

RELEASED_VERSION="${1:-}"

if [ -z "$RELEASED_VERSION" ]; then
  error "Missing arguments. Usage: ${basename} <released_version>"
fi

# Normalize version (remove 'v' prefix)
CLEAN_VERSION="${RELEASED_VERSION#v}"
RELEASED_VERSION_TAG="v${CLEAN_VERSION}"

# 1. Check for Pre-release (contains a hyphen like -rc.1)
if [[ "$CLEAN_VERSION" == *"-"* ]]; then
  msg "Info: Pre-release detected (${RELEASED_VERSION}). Skipping branch operations."
  exit 0
fi

# 2. Extract patch version (the number after the second dot)
PATCH=$(echo "$CLEAN_VERSION" | cut -d. -f3)

# 3. Check if it's a Patch release (PATCH != 0)
if [ "$PATCH" != "0" ]; then
  msg "Info: Patch release detected (${RELEASED_VERSION}). Skipping branch operations."
  exit 0
fi

# ============================================================================
# Git Setup
# ============================================================================

# Use GITHUB_TOKEN for authentication if in a CI environment
if [[ -n "$GITHUB_TOKEN" ]]; then
  msg "Configuring git to use GITHUB_TOKEN..."
  git config --global url."https://x-access-token:${GITHUB_TOKEN}@github.com/".insteadOf "https://github.com/"
fi

msg "Fetching tags from remote..."
git fetch --tags --quiet

# ============================================================================
# Functions
# ============================================================================

create_stable_branch() {
  local version=$1
  local stable_branch="stable"

  msg "Updating stable branch: $stable_branch to $version"

  git branch "$stable_branch" "$version" 2>/dev/null || git branch -f "$stable_branch" "$version"
  git push origin "$stable_branch" --force

  msg "Success: Stable branch '$stable_branch' updated."
}

create_maintenance_branch() {
  local current_tag=$1
  local clean_tag="${current_tag#v}"

  local tag_major
  local tag_minor
  tag_major=$(echo "$clean_tag" | cut -d. -f1)
  tag_minor=$(echo "$clean_tag" | cut -d. -f2)

  local maintenance_branch="release/v${tag_major}.${tag_minor}"

  # Idempotency check
  if git ls-remote --heads origin "$maintenance_branch" | grep -q "$maintenance_branch"; then
    msg "Info: Maintenance branch $maintenance_branch already exists, skipping..."
    return 0
  fi

  msg "Creating maintenance branch: $maintenance_branch from $current_tag"
  git branch "$maintenance_branch" "$current_tag"
  git push origin "$maintenance_branch"

  msg "Success: Maintenance branch $maintenance_branch created and pushed."
}

# ============================================================================
# Main Execution
# ============================================================================

msg "Detected minor/major release: $RELEASED_VERSION"

# Create stable branch
create_stable_branch "$RELEASED_VERSION_TAG"

# Create maintenance branch
create_maintenance_branch "$RELEASED_VERSION_TAG"

msg "Release branch operations completed successfully."