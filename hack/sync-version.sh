#!/bin/bash
set -e

# Usage: ./hack/sync-version.sh <version> <type>
# <version>: The semantic version (e.g., 1.2.3)
# <type>: "app" or "chart"

VERSION=$1
TYPE=$2

msg () {
  echo >&2 "[$(date +'%I:%M:%S %p')] [sync-version] $*"
}
if [ -z "$VERSION" ] || [ -z "$TYPE" ]; then
  msg "Usage: $0 <version> <type>"
  msg "  <type>: 'app' (updates appVersion) or 'chart' (updates version)"
  exit 1
fi

CHART_FILE="charts/hibernator/Chart.yaml"

if [ ! -f "$CHART_FILE" ]; then
  msg "Error: $CHART_FILE not found"
  exit 1
fi

if [ "$TYPE" == "app" ]; then
  msg "Updating appVersion to $VERSION in $CHART_FILE..."
  # Use sed to update appVersion (works on Linux and macOS)
  if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' "s/^appVersion: .*/appVersion: \"$VERSION\"/" "$CHART_FILE"
  else
    sed -i "s/^appVersion: .*/appVersion: \"$VERSION\"/" "$CHART_FILE"
  fi
elif [ "$TYPE" == "chart" ]; then
  msg "Updating chart version to $VERSION in $CHART_FILE..."
  VERSION_RAW=$(git describe --tags --abbrev=0 --match "v*" || echo "latest")
  APP_VERSION=${VERSION_RAW#v}

  if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' "s/^version: .*/version: $VERSION/" "$CHART_FILE"
    sed -i '' "s/^appVersion: .*/appVersion: ${APP_VERSION}/" "$CHART_FILE"
  else
    sed -i "s/^version: .*/version: $VERSION/" "$CHART_FILE"
    sed -i "s/^appVersion: .*/appVersion: ${APP_VERSION}/" "$CHART_FILE"
  fi
else
  msg "Error: Unknown type '$TYPE'. Use 'app' or 'chart'."
  exit 1
fi

msg "Successfully updated $CHART_FILE"

msg "Updating Helm Chart README.md..."

helm-docs -t hack/CHART_README.md.gotmpl -c charts