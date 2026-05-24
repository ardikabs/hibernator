#!/usr/bin/env bash
#
# Hibernator CLI Installer
# Usage: curl -sSL https://hibernator.ardikabs.com/install-cli.sh | bash
#        curl -sSL https://hibernator.ardikabs.com/install-cli.sh | bash -s -- --version v1.2.3
#

set -e

# Configuration
REPO="ardikabs/hibernator"
BINARY_NAME="kubectl-hibernator"
INSTALL_DIR="/usr/local/bin"
VERSION=""
FORCE="false"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Print functions
print_error() {
    echo -e "${RED}Error: $1${NC}" >&2
}

print_success() {
    echo -e "${GREEN}$1${NC}"
}

print_info() {
    echo -e "${BLUE}$1${NC}"
}

print_warning() {
    echo -e "${YELLOW}Warning: $1${NC}"
}

# Detect OS
detect_os() {
    local os
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in
        linux)
            echo "linux"
            ;;
        darwin)
            echo "darwin"
            ;;
        mingw*|cygwin*|msys*)
            echo "windows"
            ;;
        *)
            print_error "Unsupported operating system: $os"
            exit 1
            ;;
    esac
}

# Detect architecture
detect_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64)
            echo "amd64"
            ;;
        arm64|aarch64)
            echo "arm64"
            ;;
        armv7l|armv6l)
            echo "arm"
            ;;
        i386|i686)
            echo "386"
            ;;
        *)
            print_error "Unsupported architecture: $arch"
            exit 1
            ;;
    esac
}

# Get the latest version from GitHub API
get_latest_version() {
    local version
    version=$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$version" ]; then
        print_error "Could not determine latest version"
        exit 1
    fi
    echo "$version"
}

# Download the binary
download_binary() {
    local version="$1"
    local os="$2"
    local arch="$3"
    local tmp_dir="$4"
    
    local asset_name="${BINARY_NAME}_${version}_${os}_${arch}.tar.gz"
    local url="https://github.com/${REPO}/releases/download/${version}/${asset_name}"
    
    print_info "Downloading ${BINARY_NAME} ${version} for ${os}/${arch}..."
    
    if ! curl -sSL "$url" -o "${tmp_dir}/${asset_name}"; then
        print_error "Failed to download from: $url"
        print_info "Make sure version $version exists and supports ${os}/${arch}"
        exit 1
    fi
    
    echo "$asset_name"
}

# Verify checksum if available
verify_checksum() {
    local tmp_dir="$1"
    local asset_name="$2"
    local version="$3"
    
    local checksum_url="https://github.com/${REPO}/releases/download/${version}/checksums.txt"
    local checksum_file="${tmp_dir}/checksums.txt"
    
    if curl -sSL "$checksum_url" -o "$checksum_file" 2>/dev/null; then
        print_info "Verifying checksum..."
        local expected_checksum
        expected_checksum=$(grep "$asset_name" "$checksum_file" | awk '{print $1}')
        
        if [ -n "$expected_checksum" ]; then
            local actual_checksum
            if command -v sha256sum >/dev/null 2>&1; then
                actual_checksum=$(sha256sum "${tmp_dir}/${asset_name}" | awk '{print $1}')
            elif command -v shasum >/dev/null 2>&1; then
                actual_checksum=$(shasum -a 256 "${tmp_dir}/${asset_name}" | awk '{print $1}')
            else
                print_warning "Neither sha256sum nor shasum found, skipping checksum verification"
                return 0
            fi
            
            if [ "$expected_checksum" != "$actual_checksum" ]; then
                print_error "Checksum verification failed!"
                print_error "Expected: $expected_checksum"
                print_error "Actual:   $actual_checksum"
                exit 1
            fi
            print_success "Checksum verified successfully"
        fi
    else
        print_warning "Could not download checksums file, skipping verification"
    fi
}

# Extract the binary
extract_binary() {
    local tmp_dir="$1"
    local asset_name="$2"
    
    print_info "Extracting binary..."
    tar -xzf "${tmp_dir}/${asset_name}" -C "$tmp_dir"
    
    if [ ! -f "${tmp_dir}/${BINARY_NAME}" ]; then
        print_error "Binary not found in archive"
        exit 1
    fi
}

# Install the binary
install_binary() {
    local tmp_dir="$1"
    local target_path="${INSTALL_DIR}/${BINARY_NAME}"
    
    # Check if we need sudo
    local use_sudo=""
    if [ ! -w "$INSTALL_DIR" ]; then
        if command -v sudo >/dev/null 2>&1; then
            use_sudo="sudo"
            print_info "Using sudo to install to $INSTALL_DIR"
        else
            print_error "Cannot write to $INSTALL_DIR and sudo is not available"
            print_info "You can:"
            print_info "  1. Run this script with sudo"
            print_info "  2. Install to a different directory: --install-dir ~/bin"
            exit 1
        fi
    fi
    
    # Check if binary already exists
    if [ -f "$target_path" ] && [ "$FORCE" != "true" ]; then
        print_warning "Binary already exists at $target_path"
        read -p "Overwrite? [y/N] " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            print_info "Installation cancelled"
            exit 0
        fi
    fi
    
    print_info "Installing to $target_path..."
    $use_sudo mv "${tmp_dir}/${BINARY_NAME}" "$target_path"
    $use_sudo chmod +x "$target_path"
}

# Verify installation
verify_installation() {
    local target_path="${INSTALL_DIR}/${BINARY_NAME}"
    
    if [ ! -f "$target_path" ]; then
        print_error "Installation failed: binary not found at $target_path"
        exit 1
    fi
    
    # Test the binary
    if "$target_path" version >/dev/null 2>&1 || "$target_path" --version >/dev/null 2>&1 || "$target_path" -v >/dev/null 2>&1; then
        local installed_version
        installed_version=$("$target_path" version 2>/dev/null || "$target_path" --version 2>/dev/null || echo "unknown")
        print_success "Successfully installed ${BINARY_NAME}"
        print_info "Version: $installed_version"
        print_info "Location: $target_path"
    else
        print_warning "Binary installed but version check failed"
        print_info "Location: $target_path"
    fi
    
    # Check if in PATH
    if command -v "$BINARY_NAME" >/dev/null 2>&1; then
        print_success "${BINARY_NAME} is available in your PATH"
    else
        print_warning "${BINARY_NAME} is not in your PATH"
        print_info "Add the following to your shell profile:"
        print_info "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi
}

# Cleanup
cleanup() {
    local tmp_dir="$1"
    if [ -d "$tmp_dir" ]; then
        rm -rf "$tmp_dir"
    fi
}

# Print usage
usage() {
    cat <<EOF
Hibernator CLI Installer

Usage: $0 [OPTIONS]

Options:
    -v, --version VERSION    Install specific version (default: latest)
    -i, --install-dir DIR    Installation directory (default: /usr/local/bin)
    -f, --force              Force overwrite if binary exists
    -h, --help               Show this help message

Examples:
    # Install latest version
    curl -sSL https://hibernator.ardikabs.com/install-cli.sh | bash

    # Install specific version
    curl -sSL https://hibernator.ardikabs.com/install-cli.sh | bash -s -- --version v1.2.0

    # Install to custom directory
    curl -sSL https://hibernator.ardikabs.com/install-cli.sh | bash -s -- --install-dir ~/bin

EOF
}

# Parse arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -v|--version)
                VERSION="$2"
                shift 2
                ;;
            -i|--install-dir)
                INSTALL_DIR="$2"
                shift 2
                ;;
            -f|--force)
                FORCE="true"
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
}

# Main function
main() {
    parse_args "$@"
    
    print_info "========================================"
    print_info "  Hibernator CLI Installer"
    print_info "========================================"
    echo
    
    # Detect OS and architecture
    local os arch
    os=$(detect_os)
    arch=$(detect_arch)
    
    print_info "Detected: $os/$arch"
    
    # Get version
    if [ -z "$VERSION" ]; then
        print_info "Detecting latest version..."
        VERSION=$(get_latest_version)
    fi
    print_info "Version: $VERSION"
    
    # Create temp directory
    local tmp_dir
    tmp_dir=$(mktemp -d)
    trap "cleanup $tmp_dir" EXIT
    
    # Download and install
    local asset_name
    asset_name=$(download_binary "$VERSION" "$os" "$arch" "$tmp_dir")
    verify_checksum "$tmp_dir" "$asset_name" "$VERSION"
    extract_binary "$tmp_dir" "$asset_name"
    install_binary "$tmp_dir"
    verify_installation
    
    echo
    print_success "Installation complete!"
    print_info ""
    print_info "Get started with: kubectl hibernator --help"
}

# Run main function
main "$@"
