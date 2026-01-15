#!/bin/sh
# lnpm uninstaller script
# Usage: curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/uninstall.sh | sh

set -e

# Configuration
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

error() {
    printf "${RED}[ERROR]${NC} %s\n" "$1"
    exit 1
}

# Uninstall lnpm
uninstall() {
    echo ""
    echo "  _                       "
    echo " | |_ __  _ __  _ __ ___  "
    echo " | | '_ \\| '_ \\| '_ \` _ \\ "
    echo " | | | | | |_) | | | | | |"
    echo " |_|_| |_| .__/|_| |_| |_|"
    echo "         |_|              "
    echo ""
    echo " Uninstaller"
    echo ""

    LNPM_HOME="$HOME/.lnpm"

    echo "This will remove:"
    echo "  - $INSTALL_DIR/$BINARY (lnpm binary)"
    echo "  - $LNPM_HOME (lnpm store, database, and all packages)"
    echo ""
    printf "Are you sure? (type 'yes' to confirm): "
    read -r input

    if [ "$input" != "yes" ]; then
        echo "Uninstall cancelled"
        exit 0
    fi

    echo ""
    info "Uninstalling lnpm..."

    # Remove ~/.lnpm directory
    if [ -d "$LNPM_HOME" ]; then
        rm -rf "$LNPM_HOME"
        success "Removed $LNPM_HOME"
    else
        info "$LNPM_HOME not found (already removed or not initialized)"
    fi

    # Remove binary from various possible locations
    for location in "$INSTALL_DIR/$BINARY" "/usr/local/bin/$BINARY" "/usr/bin/$BINARY"; do
        if [ -f "$location" ]; then
            rm -f "$location"
            success "Removed $location"
            break
        fi
    done

    echo ""
    success "Uninstall complete"
    echo "lnpm has been removed from your system"
}

main() {
    uninstall "$@"
}

main "$@"
