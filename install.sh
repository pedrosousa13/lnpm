#!/bin/sh
# lnpm installer script
# Usage: curl -fsSL https://raw.githubusercontent.com/user/lnpm/main/install.sh | sh

set -e

# Configuration
REPO="user/lnpm"
BINARY="lnpm"
INSTALL_DIR="${LNPM_INSTALL_DIR:-$HOME/.local/bin}"

# Colors (if terminal supports it)
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    BLUE='\033[0;34m'
    NC='\033[0m' # No Color
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    NC=''
fi

info() {
    printf "${BLUE}[INFO]${NC} %s\n" "$1"
}

success() {
    printf "${GREEN}[OK]${NC} %s\n" "$1"
}

warn() {
    printf "${YELLOW}[WARN]${NC} %s\n" "$1"
}

error() {
    printf "${RED}[ERROR]${NC} %s\n" "$1"
    exit 1
}

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Linux*)     OS="linux";;
        Darwin*)    OS="darwin";;
        CYGWIN*|MINGW*|MSYS*) OS="windows";;
        *)          error "Unsupported operating system: $(uname -s)";;
    esac
}

# Detect architecture
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   ARCH="amd64";;
        arm64|aarch64)  ARCH="arm64";;
        *)              error "Unsupported architecture: $(uname -m)";;
    esac
}

# Get latest version from GitHub
get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    elif command -v wget >/dev/null 2>&1; then
        VERSION=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    else
        error "curl or wget is required"
    fi

    if [ -z "$VERSION" ]; then
        error "Failed to get latest version"
    fi

    # Remove 'v' prefix if present
    VERSION="${VERSION#v}"
}

# Download and install
install() {
    info "Installing lnpm..."

    detect_os
    detect_arch
    get_latest_version

    info "OS: $OS, Arch: $ARCH, Version: $VERSION"

    # Build download URL
    if [ "$OS" = "windows" ]; then
        FILENAME="${BINARY}_${VERSION}_${OS}_${ARCH}.zip"
    else
        FILENAME="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
    fi

    URL="https://github.com/${REPO}/releases/download/v${VERSION}/${FILENAME}"

    # Create temp directory
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT

    info "Downloading from $URL..."

    # Download
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$URL" -o "$TMP_DIR/$FILENAME" || error "Download failed"
    else
        wget -q "$URL" -O "$TMP_DIR/$FILENAME" || error "Download failed"
    fi

    # Extract
    info "Extracting..."
    cd "$TMP_DIR"
    if [ "$OS" = "windows" ]; then
        unzip -q "$FILENAME" || error "Extraction failed"
    else
        tar -xzf "$FILENAME" || error "Extraction failed"
    fi

    # Create install directory
    mkdir -p "$INSTALL_DIR"

    # Install binary
    if [ "$OS" = "windows" ]; then
        mv "${BINARY}.exe" "$INSTALL_DIR/" || error "Installation failed"
    else
        mv "$BINARY" "$INSTALL_DIR/" || error "Installation failed"
        chmod +x "$INSTALL_DIR/$BINARY"
    fi

    success "Installed lnpm v$VERSION to $INSTALL_DIR/$BINARY"

    # Check if install dir is in PATH
    case ":$PATH:" in
        *":$INSTALL_DIR:"*) ;;
        *)
            warn "$INSTALL_DIR is not in your PATH"
            echo ""
            echo "Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
            echo ""
            echo "  export PATH=\"\$PATH:$INSTALL_DIR\""
            echo ""
            ;;
    esac

    # Verify installation
    if [ -x "$INSTALL_DIR/$BINARY" ]; then
        echo ""
        success "Installation complete!"
        echo ""
        echo "Run 'lnpm --help' to get started"
    fi
}

# Main
main() {
    echo ""
    echo "  _                       "
    echo " | |_ __  _ __  _ __ ___  "
    echo " | | '_ \\| '_ \\| '_ \` _ \\ "
    echo " | | | | | |_) | | | | | |"
    echo " |_|_| |_| .__/|_| |_| |_|"
    echo "         |_|              "
    echo ""
    echo " Fast local npm package development"
    echo ""

    install
}

main "$@"
