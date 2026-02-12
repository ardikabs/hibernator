#!/bin/bash
set -e

# Usage: ./hack/sync-version.sh <version> <type>
# <version>: The semantic version (e.g., 1.2.3)
# <type>: "app" or "chart"

VERSION=$1
TYPE=$2

if [ -z "$VERSION" ] || [ -z "$TYPE" ]; then
  echo "Usage: $0 <version> <type>"
  echo "  <type>: 'app' (updates appVersion) or 'chart' (updates version)"
  exit 1
fi

CHART_FILE="charts/hibernator/Chart.yaml"

if [ ! -f "$CHART_FILE" ]; then
  echo "Error: $CHART_FILE not found"
  exit 1
fi

if [ "$TYPE" == "app" ]; then
  echo "Updating appVersion to $VERSION in $CHART_FILE..."
  # Use sed to update appVersion (works on Linux and macOS)
  if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' "s/^appVersion: .*/appVersion: \"$VERSION\"/" "$CHART_FILE"
  else
    sed -i "s/^appVersion: .*/appVersion: \"$VERSION\"/" "$CHART_FILE"
  fi
elif [ "$TYPE" == "chart" ]; then
  echo "Updating chart version to $VERSION in $CHART_FILE..."
  APP_VERSION=$(git describe --tags --abbrev=0 --match "v*")

  if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' "s/^version: .*/version: $VERSION/" "$CHART_FILE"
    sed -i '' "s/^appVersion: .*/appVersion: ${APP_VERSION}/" "$CHART_FILE"
  else
    sed -i "s/^version: .*/version: $VERSION/" "$CHART_FILE"
    sed -i "s/^appVersion: .*/appVersion: ${APP_VERSION}/" "$CHART_FILE"
  fi
else
  echo "Error: Unknown type '$TYPE'. Use 'app' or 'chart'."
  exit 1
fi

echo "Successfully updated $CHART_FILE"

echo >&2 "Updating Helm Chart README.md..."

helm-docs -t hack/CHART_README.md.gotmpl -c charts