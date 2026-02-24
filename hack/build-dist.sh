#!/bin/bash

set -euo pipefail

help() {
cat <<EOF
Build kubectl-hibernator CLI distribution for all platforms
Usage: ./hack/build-dist.sh [OPTIONS]

Options:
  --version VERSION         Version to embed (default: dev)
  --commit COMMIT_HASH      Commit hash to embed (default: current HEAD)
  --output DIR              Output directory (default: dist/kubectl-hibernator/<VERSION>)
  --go-cmd CMD              Go command to use (default: go)
  --platforms PLATFORMS     Platforms to build (default: darwin/amd64 darwin/arm64 linux/amd64 linux/arm64)
  --help                    Show this help message
EOF
  exit 0
}

basename="${0##*/}"
toolname="${basename%.*}"

msg () {
  echo >&2 -e "[$(date +'%-I:%M:%S %p')] [${toolname}] $*"
}

# Color codes
readonly CYAN='\033[36m'
readonly GREEN='\033[32m'
readonly RED='\033[31m'
readonly RESET='\033[0m'

# Defaults
VERSION="${VERSION:-dev}"
COMMIT_HASH="${COMMIT_HASH:-$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")}"
OUTPUT_DIR="${OUTPUT_DIR:-}"
GO_CMD="${GO_CMD:-go}"
PLATFORMS="${PLATFORMS:-darwin/amd64 darwin/arm64 linux/amd64 linux/arm64}"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="$2"
      shift 2
      ;;
    --commit)
      COMMIT_HASH="$2"
      shift 2
      ;;
    --output)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --go-cmd)
      GO_CMD="$2"
      shift 2
      ;;
    --platforms)
      PLATFORMS="$2"
      shift 2
      ;;
    --help)
      help
      ;;
    *)
      msg "${RED}Error: Unknown option: $1${RESET}"
      exit 1
      ;;
  esac
done

# Set defaults for unset parameters
if [ -z "$COMMIT_HASH" ]; then
  COMMIT_HASH=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
fi

if [ -z "$OUTPUT_DIR" ]; then
  OUTPUT_DIR="dist/kubectl-hibernator/${VERSION}"
fi


# Create output directory
mkdir -p "$OUTPUT_DIR"

msg "${CYAN}Building CLI distribution for all platforms (version=${VERSION})...${RESET}"
msg "${CYAN}Output directory: ${OUTPUT_DIR}${RESET}"
msg ""

# Build LDFLAGS
LDFLAGS=(-ldflags "-X github.com/ardikabs/hibernator/internal/version.Version=${VERSION} -X github.com/ardikabs/hibernator/internal/version.CommitHash=${COMMIT_HASH}")

# Build for each platform
for platform in $PLATFORMS; do
  OS=$(echo "$platform" | cut -d'/' -f1)
  ARCH=$(echo "$platform" | cut -d'/' -f2)
  BINARY_NAME="kubectl-hibernator-${OS}-${ARCH}"
  BINARY_PATH="${OUTPUT_DIR}/${BINARY_NAME}"

  msg "${CYAN}  Building for ${OS}/${ARCH}...${RESET}"

  # shellcheck disable=SC2086
  CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH" $GO_CMD build "${LDFLAGS[@]}" -o "$BINARY_PATH" ./cmd/kubectl-hibernator

  if [ -f "$BINARY_PATH" ]; then
    SIZE=$(ls -lh "$BINARY_PATH" | awk '{print $5}')
    msg "${GREEN}    ✓ Built: ${BINARY_NAME} (${SIZE})${RESET}"
  else
    msg "${RED}    ✗ Failed to build: ${BINARY_NAME}${RESET}"
    exit 1
  fi
done

msg ""
msg "${GREEN}CLI distribution built in ${OUTPUT_DIR}${RESET}"
msg "${GREEN}Run: ls -lh ${OUTPUT_DIR}${RESET}"

# Set outputs for GitHub Actions
echo "new_release_git_sha_short=${COMMIT_HASH}" >> "${GITHUB_OUTPUT:-/dev/null}"