#!/bin/sh
# lnpm installer script
# Usage: curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

set -e

# Configuration
REPO="pedrosousa13/lnpm"
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

# Verify the archive's SHA-256 against the release checksums.txt.
# checksums.txt comes from the same release as the archive, so this catches a
# corrupted download or an archive altered in transit - not a release where an
# attacker replaced both files.
# sh has no 'local', so the variables below are prefixed to keep them scoped to
# this helper by convention.
verify_checksum() {
    VC_DIR="$1"
    VC_FILE="$2"

    if command -v sha256sum >/dev/null 2>&1; then
        VC_SHA_CMD="sha256sum"
    elif command -v shasum >/dev/null 2>&1; then
        VC_SHA_CMD="shasum -a 256"
    else
        error "sha256sum or shasum is required to verify the download"
    fi

    if [ ! -r "$VC_DIR/checksums.txt" ]; then
        error "checksums.txt is missing or unreadable"
    fi

    # goreleaser writes "<hex>  <filename>", one entry per line. The CR is
    # stripped so a CRLF checksums.txt both matches here and hashes below.
    VC_ENTRY=$(awk -v f="$VC_FILE" '{ sub(/\r$/, "") } $2 == f { print; exit }' "$VC_DIR/checksums.txt")
    if [ -z "$VC_ENTRY" ]; then
        error "No checksum listed for $VC_FILE in checksums.txt"
    fi

    # VC_SHA_CMD is unquoted on purpose: it may carry arguments.
    if ! printf '%s\n' "$VC_ENTRY" | (cd "$VC_DIR" && $VC_SHA_CMD -c -) >/dev/null 2>&1; then
        error "Checksum mismatch for $VC_FILE - the download is corrupted or was altered"
    fi
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
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt"

    # Create temp directory
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT

    info "Downloading from $URL..."

    # Download
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$URL" -o "$TMP_DIR/$FILENAME" || error "Download failed"
        curl -fsSL "$CHECKSUMS_URL" -o "$TMP_DIR/checksums.txt" || error "Failed to download checksums.txt"
    else
        wget -q "$URL" -O "$TMP_DIR/$FILENAME" || error "Download failed"
        wget -q "$CHECKSUMS_URL" -O "$TMP_DIR/checksums.txt" || error "Failed to download checksums.txt"
    fi

    # Verify before extracting
    info "Verifying checksum..."
    verify_checksum "$TMP_DIR" "$FILENAME"
    success "Checksum verified"

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
