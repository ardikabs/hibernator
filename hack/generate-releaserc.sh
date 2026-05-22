#!/usr/bin/env bash

# hack/generate-releaserc.sh
# Dynamically generates .releaserc.yml based on branch context and release intent.

set -e

CURRENT_BRANCH=$1
EVENT_NAME=$2 # workflow_dispatch, workflow_run, or push
BASE_FILE=".github/releaserc/base.releaserc.yml"

if [[ -z "$CURRENT_BRANCH" || -z "$EVENT_NAME" ]]; then
    echo "Usage: $0 <branch-name> <event-name>"
    exit 1
fi

if [[ ! -f "$BASE_FILE" ]]; then
    echo "Error: Base template $BASE_FILE not found."
    exit 1
fi

IS_MANUAL=false
if [[ "$EVENT_NAME" == "workflow_dispatch" ]]; then
    IS_MANUAL=true
fi

msg() {
  echo >&2 "$*"
}

# 1. Identify the Latest Maintenance Branch using Version Sort
# This ensures v1.10 is considered "newer" than v1.9
LATEST_MAINTENANCE=$(git branch -r | grep 'origin/release/v' | sed 's/origin\///' | sort -Vr | head -n 1 | xargs)

msg "--- Release Context ---"
msg "Branch: $CURRENT_BRANCH"
msg "Event:  $EVENT_NAME (Manual: $IS_MANUAL)"
msg "Latest Maintenance: $LATEST_MAINTENANCE"

# 2. Build the Dynamic 'branches' YAML section
TEMP_BRANCHES="/tmp/branches.tmp.yml"
echo "branches:" >> $TEMP_BRANCHES

if [[ "$CURRENT_BRANCH" == "main" ]]; then
    if [[ "$IS_MANUAL" == "true" ]]; then
        # Manual Trigger on main: main is the Stable source
cat <<EOF >> $TEMP_BRANCHES
  - main
EOF
    else
        # Automated (CI) on main: stable is baseline, main is RC
cat <<EOF >> $TEMP_BRANCHES
  - {"name": "stable", "channel": "stable"}
  - {"name": "main", "prerelease": "rc"}
EOF
    fi

    # Always include the pattern for maintenance branches for reference
cat <<EOF >> $TEMP_BRANCHES
  - {"name": "release/v+([0-9]).+([0-9])", "range": "\${name.replace('release/v', '') + '.x'}", "channel": "\${name.replace('release/v', '')}"}
EOF

elif [[ "$CURRENT_BRANCH" == "$LATEST_MAINTENANCE" ]]; then
    # Patching the LATEST maintenance branch
    # We omit 'main' to prevent semantic-release from seeing 1.8.0-rc on trunk
    # and blocking a 1.7.1 patch on this branch.
cat <<EOF >> $TEMP_BRANCHES
  - "$CURRENT_BRANCH"
EOF

else
cat <<EOF >> $TEMP_BRANCHES
  - main
  - {"name": "release/v+([0-9]).+([0-9])", "range": "\${name.replace('release/v', '') + '.x'}", "channel": "\${name.replace('release/v', '')}"}
EOF
fi

sed -e "/# BRANCHES_PLACEHOLDER/r $TEMP_BRANCHES" -e "/# BRANCHES_PLACEHOLDER/d" "$BASE_FILE" > .releaserc.yml
rm $TEMP_BRANCHES

msg "✅ Generated .releaserc.yml for $CURRENT_BRANCH."