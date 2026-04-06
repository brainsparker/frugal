#!/usr/bin/env bash
set -euo pipefail

# frugal.sh installer
# Usage: curl -fsSL https://frugal.sh/install | sh

REPO="frugalsh/frugal"
INSTALL_DIR="${FRUGAL_INSTALL_DIR:-$HOME/.frugal}"
BIN_DIR="$INSTALL_DIR/bin"
CONFIG_DIR="$INSTALL_DIR/config"

# ---- helpers ----

info()  { printf "\033[1;34m==>\033[0m %s\n" "$1"; }
ok()    { printf "\033[1;32m ✓\033[0m  %s\n" "$1"; }
warn()  { printf "\033[1;33m !\033[0m  %s\n" "$1"; }
fail()  { printf "\033[1;31m ✗\033[0m  %s\n" "$1" >&2; exit 1; }

detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) fail "unsupported architecture: $arch" ;;
    esac

    case "$os" in
        linux)  echo "linux-${arch}" ;;
        darwin) echo "darwin-${arch}" ;;
        *)      fail "unsupported OS: $os" ;;
    esac
}

latest_version() {
    if command -v curl &>/dev/null; then
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4
    elif command -v wget &>/dev/null; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4
    else
        fail "curl or wget required"
    fi
}

download() {
    local url="$1" dest="$2"
    if command -v curl &>/dev/null; then
        curl -fsSL "$url" -o "$dest"
    else
        wget -qO "$dest" "$url"
    fi
}

# ---- install ----

main() {
    info "installing frugal.sh — the open-source LLM cost optimizer"
    echo

    # Detect platform
    local platform
    platform="$(detect_platform)"
    ok "detected platform: $platform"

    # Get latest version (or build from source if no releases yet)
    local version
    version="$(latest_version 2>/dev/null || echo "")"

    mkdir -p "$BIN_DIR" "$CONFIG_DIR"

    if [ -n "$version" ]; then
        info "downloading frugal $version for $platform..."
        local url="https://github.com/${REPO}/releases/download/${version}/frugal-${platform}"
        download "$url" "$BIN_DIR/frugal"
        chmod +x "$BIN_DIR/frugal"
        ok "downloaded frugal $version"
    else
        # No releases yet — build from source
        info "no release found, building from source..."
        if ! command -v go &>/dev/null; then
            fail "go is required to build from source (install: https://go.dev/dl/)"
        fi

        local tmpdir
        tmpdir="$(mktemp -d)"
        trap "rm -rf $tmpdir" EXIT

        if command -v git &>/dev/null; then
            git clone --depth 1 "https://github.com/${REPO}.git" "$tmpdir/frugal" 2>/dev/null
        else
            download "https://github.com/${REPO}/archive/refs/heads/main.tar.gz" "$tmpdir/frugal.tar.gz"
            tar -xzf "$tmpdir/frugal.tar.gz" -C "$tmpdir"
            mv "$tmpdir/frugal-main" "$tmpdir/frugal"
        fi

        (cd "$tmpdir/frugal" && go build -o "$BIN_DIR/frugal" ./cmd/frugal)
        cp "$tmpdir/frugal/config/models.yaml" "$CONFIG_DIR/models.yaml"
        ok "built frugal from source"
    fi

    # Download default config if not present
    if [ ! -f "$CONFIG_DIR/models.yaml" ]; then
        info "downloading model config..."
        download "https://raw.githubusercontent.com/${REPO}/main/config/models.yaml" "$CONFIG_DIR/models.yaml"
        ok "config saved to $CONFIG_DIR/models.yaml"
    fi

    echo
    info "detecting API keys..."

    local keys_found=0
    [ -n "${OPENAI_API_KEY:-}" ]    && { ok "OPENAI_API_KEY found";    keys_found=$((keys_found + 1)); }
    [ -n "${ANTHROPIC_API_KEY:-}" ] && { ok "ANTHROPIC_API_KEY found"; keys_found=$((keys_found + 1)); }
    [ -n "${GOOGLE_API_KEY:-}" ]    && { ok "GOOGLE_API_KEY found";    keys_found=$((keys_found + 1)); }

    if [ "$keys_found" -eq 0 ]; then
        warn "no API keys found in environment"
        echo "  Set at least one of: OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY"
        echo "  Then run: frugal"
        echo
    fi

    # Add to PATH
    local shell_config=""
    local export_line="export PATH=\"$BIN_DIR:\$PATH\""
    local config_line="export FRUGAL_CONFIG=\"$CONFIG_DIR/models.yaml\""

    if [ -f "$HOME/.zshrc" ]; then
        shell_config="$HOME/.zshrc"
    elif [ -f "$HOME/.bashrc" ]; then
        shell_config="$HOME/.bashrc"
    elif [ -f "$HOME/.bash_profile" ]; then
        shell_config="$HOME/.bash_profile"
    fi

    if [ -n "$shell_config" ]; then
        if ! grep -q ".frugal/bin" "$shell_config" 2>/dev/null; then
            echo "" >> "$shell_config"
            echo "# frugal.sh" >> "$shell_config"
            echo "$export_line" >> "$shell_config"
            echo "$config_line" >> "$shell_config"
            ok "added to PATH in $shell_config"
        fi
    fi

    # Also export for current session
    export PATH="$BIN_DIR:$PATH"
    export FRUGAL_CONFIG="$CONFIG_DIR/models.yaml"

    echo
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo
    echo "  frugal.sh installed!"
    echo
    echo "  Start the proxy:"
    echo "    frugal"
    echo
    echo "  Then point your app at it:"
    echo "    export OPENAI_BASE_URL=http://localhost:8080/v1"
    echo
    echo "  That's it. Same code. Same SDK. Lower bill."
    echo
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo
}

main "$@"
