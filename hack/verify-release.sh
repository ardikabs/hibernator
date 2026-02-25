#!/bin/bash

# Script: verify-release.sh
# Purpose: Guard against minor version bumps
#
# Blocks (exits 1) if release type is "minor"
# Allows patch and major releases
#
# Usage: verify-release.sh <release_type>
# Example: verify-release.sh minor  # exits 1
# Example: verify-release.sh patch  # exits 0

set -e

readonly basename="${0##*/}"
readonly scriptname="${basename%.*}"

# ============================================================================
# Utility Functions
# ============================================================================

msg() {
  echo >&2 "[$(date +'%-I:%M:%S %p')] [$scriptname] $*"
}

# ============================================================================
# Input Validation
# ============================================================================

RELEASE_TYPE="${1:-}"

if [ -z "$RELEASE_TYPE" ]; then
  msg "Error: Missing arguments. Usage: ${basename} <release_type>"
  exit 1
fi

# ============================================================================
# Check Current Branch
# ============================================================================

CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)

# ============================================================================
# Guard Against Major/Minor Bumps on Maintenance Branches
# ============================================================================

if [ "$CURRENT_BRANCH" != "main" ]; then
  if [ "$RELEASE_TYPE" == "major" ] || [ "$RELEASE_TYPE" == "minor" ]; then
    msg "Error: $RELEASE_TYPE version bumps are not allowed on branch '$CURRENT_BRANCH'."
    msg "Only patch releases are permitted on maintenance branches."
    exit 1
  fi
fi

msg "Info: Release type '$RELEASE_TYPE' is allowed on branch '$CURRENT_BRANCH'."
exit 0
