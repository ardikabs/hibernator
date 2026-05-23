#!/usr/bin/env bash

set -e

# --- Configuration & Inputs ---
CURRENT_BRANCH=$1
EVENT_NAME=$2
BASE_FILE=".github/releaserc/base.releaserc.yml"
OUTPUT_FILE=".releaserc.yml"

# Templates
TEMP_BRANCHES=$(mktemp)
TEMP_PLUGINS=$(mktemp)

# --- Validation ---
if [[ -z "$CURRENT_BRANCH" || -z "$EVENT_NAME" ]]; then
    echo "Usage: $0 <branch-name> <event-name>"
    exit 1
fi

if [[ ! -f "$BASE_FILE" ]]; then
    echo "Error: Base template $BASE_FILE not found."
    exit 1
fi

msg() { echo >&2 "$*"; }

# 1. Identify Latest Maintenance Branch
LATEST_MAINTENANCE=$(git branch -r | grep 'origin/release/v' | sed 's/origin\///' | sort -Vr | head -n 1 | xargs)

# 2. Scoped Generator: Branches Configuration
generate_branches_config() {
    msg "Determining branch strategy for: $CURRENT_BRANCH"

    # Local Template Definitions
    local main_only="  - main"
    local stable_rc
    stable_rc=$(cat <<EOF
  - name: "stable"
    channel: "stable"
  - name: "main"
    prerelease: "rc"
EOF
    )

    # Note: We escape the $ here so Bash treats it as a literal for semantic-release
    local maintenance_pattern
    maintenance_pattern=$(cat <<EOF
  - name: "release/v+([0-9]).+([0-9])"
    range: "\${name.replace('release/v', '') + '.x'}"
    channel: "\${name.replace('release/v', '')}"
EOF
    )

    local result=""

    if [[ "$CURRENT_BRANCH" == "main" ]]; then
        if [[ "$EVENT_NAME" == "workflow_dispatch" ]]; then
            result="$main_only"
        else
            result="$stable_rc"
        fi
        result="$result"$'\n'"$maintenance_pattern"

    elif [[ "$CURRENT_BRANCH" == "$LATEST_MAINTENANCE" ]]; then
        result="  - \"$CURRENT_BRANCH\""

    else
        result="$main_only"$'\n'"$maintenance_pattern"
    fi

    echo "branches:" > "$TEMP_BRANCHES"
    echo "$result" >> "$TEMP_BRANCHES"
}

# 3. Final Assembly
assemble_config() {
    # Fix: Pipe the output so both placeholders are replaced in one flow
    sed -e "/# BRANCHES_PLACEHOLDER/r $TEMP_BRANCHES" \
        -e "/# BRANCHES_PLACEHOLDER/d" "$BASE_FILE" | \
    sed -e "/# COMMITS_PLACEHOLDER/r $TEMP_PLUGINS" \
        -e "/# COMMITS_PLACEHOLDER/d" > "$OUTPUT_FILE"

    rm "$TEMP_BRANCHES" "$TEMP_PLUGINS"
}

# --- Execution ---
generate_branches_config
assemble_config

msg "✅ Generated $OUTPUT_FILE for $CURRENT_BRANCH."