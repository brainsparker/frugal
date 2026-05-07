#!/usr/bin/env bash
#
# frugal.sh installer
# Usage:
#   curl -fsSL https://frugal.sh/install | bash
#   curl -fsSL https://frugal.sh/install | bash -s uninstall
#
# Pipe to `bash`, not `sh`. On Ubuntu/Debian /bin/sh is dash and doesn't
# support `set -o pipefail` or other bash-isms used below. The shebang is
# ignored when the script is streamed from stdin, so the interpreter is
# whatever you pipe to.
#
# Env vars:
#   FRUGAL_VERSION      Pin a specific release tag (e.g. v0.1.0). Default: latest.
#   FRUGAL_INSTALL_DIR  Install root. Default: $HOME/.frugal
#   FRUGAL_YES          Non-interactive. Skips the confirmation prompt.
#   GITHUB_TOKEN        Optional. When set, the releases/latest API call is
#                       authenticated (5000/hr cap instead of 60/hr). Useful
#                       in CI where runner IPs are shared.
#
# Exit codes:
#   0  success
#   2  unsupported platform
#   3  network / upstream failure
#   4  verification (checksum or signature) failed
#   5  local state / user-aborted

set -euo pipefail

readonly EXIT_UNSUPPORTED=2
readonly EXIT_NETWORK=3
readonly EXIT_VERIFY=4
readonly EXIT_LOCAL=5

readonly REPO="brainsparker/frugal"
readonly PINNED_VERSION="${FRUGAL_VERSION:-}"
readonly INSTALL_DIR="${FRUGAL_INSTALL_DIR:-$HOME/.frugal}"
readonly BIN_DIR="$INSTALL_DIR/bin"
readonly CONFIG_DIR="$INSTALL_DIR/config"

# Exact-match markers for the shell rc block. Uninstall deletes everything
# between (and including) these lines. Do not change these strings without
# considering existing users — the uninstall path depends on matching them.
readonly RC_BEGIN="# >>> frugal.sh >>>"
readonly RC_END="# <<< frugal.sh <<<"

# ---- UI ----

info() { printf "\033[1;34m==>\033[0m %s\n" "$1"; }
ok()   { printf "\033[1;32m ✓\033[0m  %s\n" "$1"; }
warn() { printf "\033[1;33m !\033[0m  %s\n" "$1"; }
fail() { printf "\033[1;31m ✗\033[0m  %s\n" "$1" >&2; exit "${2:-1}"; }

# ---- Platform detection ----

detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$arch" in
        x86_64|amd64)  arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) fail "unsupported architecture: $arch" "$EXIT_UNSUPPORTED" ;;
    esac

    case "$os" in
        linux)  echo "linux-${arch}" ;;
        darwin) echo "darwin-${arch}" ;;
        *) fail "unsupported OS: $os (supported: macOS, Linux)" "$EXIT_UNSUPPORTED" ;;
    esac
}

# ---- Network helpers ----

http_get() {
    # Fetch URL to stdout. Loudly on any non-2xx or connection error.
    local url="$1"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" || fail "failed to fetch $url" "$EXIT_NETWORK"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$url" || fail "failed to fetch $url" "$EXIT_NETWORK"
    else
        fail "curl or wget is required" "$EXIT_NETWORK"
    fi
}

http_download() {
    local url="$1" dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$dest" || fail "failed to download $url" "$EXIT_NETWORK"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$dest" "$url" || fail "failed to download $url" "$EXIT_NETWORK"
    else
        fail "curl or wget is required" "$EXIT_NETWORK"
    fi
}

# ---- Version resolution ----

resolve_version() {
    if [ -n "$PINNED_VERSION" ]; then
        echo "$PINNED_VERSION"
        return
    fi
    local api_url="https://api.github.com/repos/${REPO}/releases/latest"
    local json tag
    # Unauthenticated GitHub API requests are rate-limited to 60/hour per IP.
    # Shared CI runner pools blow that cap easily. Honour GITHUB_TOKEN when
    # present (bumps the cap to 5000/hour) — silent no-op for end users.
    if [ -n "${GITHUB_TOKEN:-}" ] && command -v curl >/dev/null 2>&1; then
        json="$(curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "$api_url")" \
            || fail "failed to fetch $api_url" "$EXIT_NETWORK"
    else
        json="$(http_get "$api_url")"
    fi
    if command -v jq >/dev/null 2>&1; then
        tag="$(printf '%s' "$json" | jq -r '.tag_name // empty')"
    else
        # Strict anchored regex; fails loudly if the JSON shape shifts so we
        # never silently install the wrong version.
        tag="$(printf '%s' "$json" | sed -nE 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' | head -n1)"
    fi
    [ -n "$tag" ] || fail "could not resolve latest version (API response missing tag_name)" "$EXIT_NETWORK"
    echo "$tag"
}

# ---- Checksum ----

sha256_check() {
    # Verify that a file matches its line in a SHA256SUMS file.
    # Linux has sha256sum; macOS has shasum -a 256. Both accept -c on stdin.
    local file="$1" sums="$2" base tool
    base="$(basename "$file")"

    if command -v sha256sum >/dev/null 2>&1; then
        tool=(sha256sum -c -)
    elif command -v shasum >/dev/null 2>&1; then
        tool=(shasum -a 256 -c -)
    else
        fail "no sha256 tool found (need sha256sum or shasum)" "$EXIT_VERIFY"
    fi

    (
        cd "$(dirname "$file")" &&
        grep " ${base}\$" "$sums" | "${tool[@]}" >/dev/null
    ) || fail "sha256 mismatch for $base" "$EXIT_VERIFY"
}

# ---- Shell rc editing ----

detect_shell_rc() {
    # Prefer the rc matching the current login shell. Falls back to the first
    # existing rc file. Returns empty if none match (caller warns and skips).
    case "${SHELL:-}" in
        */zsh)  echo "$HOME/.zshrc";  return ;;
        */bash) echo "$HOME/.bashrc"; return ;;
    esac
    for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.bash_profile"; do
        [ -f "$rc" ] && { echo "$rc"; return; }
    done
}

remove_rc_block() {
    local rc="$1"
    [ -f "$rc" ] || return 0
    grep -qxF "$RC_BEGIN" "$rc" || return 0
    # Portable block-delete: awk handles BSD/GNU sed differences.
    awk -v b="$RC_BEGIN" -v e="$RC_END" '
        $0 == b { skip = 1; next }
        $0 == e { skip = 0; next }
        !skip
    ' "$rc" > "$rc.frugal.tmp" && mv "$rc.frugal.tmp" "$rc"
}

write_rc_block() {
    local rc="$1"
    remove_rc_block "$rc"
    # >> creates the file if it doesn't exist (fresh-Mac with no ~/.zshrc case).
    {
        echo ""
        echo "$RC_BEGIN"
        echo "# Added by frugal.sh installer. Remove this block to uninstall PATH."
        echo "export PATH=\"$BIN_DIR:\$PATH\""
        echo "export FRUGAL_CONFIG=\"$CONFIG_DIR/models.yaml\""
        echo "$RC_END"
    } >> "$rc"
}

# ---- Uninstall ----

uninstall() {
    info "uninstalling frugal.sh"

    if [ -d "$INSTALL_DIR" ]; then
        # Guardrail: only rm -rf paths that look like a Frugal install dir.
        # Belt-and-suspenders against a mis-set FRUGAL_INSTALL_DIR.
        case "$INSTALL_DIR" in
            "$HOME/.frugal"|*/.frugal|*/frugal)
                rm -rf "$INSTALL_DIR"
                ok "removed $INSTALL_DIR"
                ;;
            *)
                warn "refusing to remove unexpected INSTALL_DIR: $INSTALL_DIR"
                warn "remove it by hand if you meant to"
                ;;
        esac
    fi

    for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.bash_profile"; do
        if [ -f "$rc" ] && grep -qxF "$RC_BEGIN" "$rc"; then
            remove_rc_block "$rc"
            ok "cleaned $rc"
        fi
    done

    echo
    echo "frugal.sh uninstalled."
    exit 0
}

# ---- Install ----

main() {
    if [ "${1:-}" = "uninstall" ]; then
        uninstall
    fi

    info "installing frugal.sh — the open-source AI toolchain cost optimizer"
    echo

    local platform version
    platform="$(detect_platform)"
    ok "detected platform: $platform"

    info "resolving version..."
    version="$(resolve_version)"
    ok "target version: $version"

    local shell_config
    shell_config="$(detect_shell_rc || true)"

    # Show what's about to happen. Interactive sessions get a prompt;
    # FRUGAL_YES=1 and non-TTY runs (e.g. CI, curl-pipe-sh) skip it.
    echo
    echo "This installer will:"
    echo "  * install frugal $version to $BIN_DIR/frugal"
    echo "  * write a marker block to ${shell_config:-<none found; skipping>} for PATH + FRUGAL_CONFIG"
    echo "  * leave default config at $CONFIG_DIR/models.yaml"
    if [ -t 0 ] && [ "${FRUGAL_YES:-}" != "1" ]; then
        printf "Proceed? [Y/n] "
        local answer
        read -r answer </dev/tty || answer="Y"
        case "$answer" in
            ""|y|Y|yes|Yes) ;;
            *) fail "aborted by user" "$EXIT_LOCAL" ;;
        esac
    fi
    echo

    # Every download goes through a tmpdir. The final binary lands in BIN_DIR
    # only after verification succeeds. If the script exits early, the tmpdir
    # is cleaned and BIN_DIR is never polluted with an untrusted binary.
    #
    # NOTE: tmpdir is NOT declared `local` — the EXIT trap fires after main()
    # returns, at which point a function-local would be out of scope and
    # `set -u` would error on the unbound reference, making a successful
    # install exit 1.
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT

    mkdir -p "$BIN_DIR" "$CONFIG_DIR"

    local base="https://github.com/${REPO}/releases/download/${version}"
    local artifact="frugal-${platform}"

    info "downloading $artifact..."
    http_download "${base}/${artifact}" "$tmpdir/${artifact}"
    http_download "${base}/SHA256SUMS"  "$tmpdir/SHA256SUMS"

    # Trust chain: cosign -> SHA256SUMS -> binary hash -> binary.
    # Cosign is preferred; when it's not installed we keep installing (don't
    # block first-time users on a new dependency) but say so loudly.
    if command -v cosign >/dev/null 2>&1; then
        http_download "${base}/SHA256SUMS.sig" "$tmpdir/SHA256SUMS.sig"
        cosign verify-blob \
            --bundle "$tmpdir/SHA256SUMS.sig" \
            --certificate-identity-regexp "https://github.com/${REPO}/.github/workflows/release.yml@refs/tags/" \
            --certificate-oidc-issuer https://token.actions.githubusercontent.com \
            "$tmpdir/SHA256SUMS" >/dev/null \
            || fail "cosign signature verification failed for SHA256SUMS" "$EXIT_VERIFY"
        ok "cosign signature verified"
    else
        warn "cosign not found — signature check skipped"
        warn "install cosign to enable: https://docs.sigstore.dev/cosign/installation/"
    fi

    sha256_check "$tmpdir/$artifact" "$tmpdir/SHA256SUMS"
    ok "checksum verified"

    # Atomic promotion: one mv, not a copy + chmod dance.
    chmod +x "$tmpdir/$artifact"
    mv "$tmpdir/$artifact" "$BIN_DIR/frugal"
    ok "installed frugal $version to $BIN_DIR/frugal"

    # Default config: fetch only if missing so re-runs don't clobber edits.
    if [ ! -f "$CONFIG_DIR/models.yaml" ]; then
        info "downloading default model config..."
        http_download "https://raw.githubusercontent.com/${REPO}/main/config/models.yaml" \
                      "$CONFIG_DIR/models.yaml"
        ok "default config saved to $CONFIG_DIR/models.yaml"
    else
        ok "config already present at $CONFIG_DIR/models.yaml (kept)"
    fi

    # Shell rc wiring.
    if [ -n "$shell_config" ]; then
        write_rc_block "$shell_config"
        ok "shell config updated: $shell_config"
    else
        warn "no shell rc file found; add this to your shell profile:"
        echo "    export PATH=\"$BIN_DIR:\$PATH\""
        echo "    export FRUGAL_CONFIG=\"$CONFIG_DIR/models.yaml\""
    fi

    # Export for this process so the smoke test below finds the binary.
    export PATH="$BIN_DIR:$PATH"
    export FRUGAL_CONFIG="$CONFIG_DIR/models.yaml"

    # Post-install smoke test: if --version doesn't respond, something's off
    # even if every prior step reported success (corrupt file on disk, wrong
    # arch artifact, exec bit stripped by a weird umask, etc).
    if "$BIN_DIR/frugal" --version >/dev/null 2>&1; then
        ok "smoke test: frugal --version OK"
    else
        fail "smoke test failed: $BIN_DIR/frugal --version did not exit cleanly" "$EXIT_VERIFY"
    fi

    # Key detection — informational only.
    echo
    info "detecting provider API keys..."
    local keys=0
    [ -n "${OPENAI_API_KEY:-}"    ] && { ok "OPENAI_API_KEY found";    keys=$((keys + 1)); }
    [ -n "${ANTHROPIC_API_KEY:-}" ] && { ok "ANTHROPIC_API_KEY found"; keys=$((keys + 1)); }
    [ -n "${GOOGLE_API_KEY:-}"    ] && { ok "GOOGLE_API_KEY found";    keys=$((keys + 1)); }
    echo
    printf '\033[2m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m\n'
    echo
    printf '  \033[1;32m✓\033[0m  \033[1mfrugal.sh installed\033[0m  \033[2m·  %s  ·  %s\033[0m\n' "$version" "$platform"
    echo

    if [ "$keys" -eq 0 ]; then
        printf '  \033[1;33m⚠\033[0m  no provider API keys detected — the proxy can'\''t make calls yet.\n'
        echo
        printf '     \033[2mFrugal has no SaaS or account — use the keys you already have\n'
        printf '     from your model / toolchain provider.\033[0m\n'
        echo
        printf '  \033[2m─── \033[0m\033[1;36mSet a key to start\033[0m\033[2m ──────────────────────────────\033[0m\n'
        echo
        printf '  \033[1;32m▸\033[0m  cheapest tier  \033[2m(Gemini Flash — pennies per million tokens)\033[0m\n'
        printf '        \033[1m$\033[0m export GOOGLE_API_KEY=...\n'
        echo
        printf '  \033[1;32m▸\033[0m  Anthropic\n'
        printf '        \033[1m$\033[0m export ANTHROPIC_API_KEY=...\n'
        echo
        printf '  \033[1;32m▸\033[0m  OpenAI\n'
        printf '        \033[1m$\033[0m export OPENAI_API_KEY=...\n'
        echo
        printf '  Then start the proxy:\n'
        printf '        \033[1m$\033[0m frugal serve\n'
        echo
        printf '  \033[2mNot sure yet?  See the live benchmark →\033[0m \033[1;36mhttps://frugal.sh/benchmark\033[0m\n'
    else
        printf '  \033[2m─── \033[0m\033[1;36mTry it\033[0m\033[2m ──────────────────────────────────────────\033[0m\n'
        echo
        printf '  \033[1;32m1.\033[0m  \033[1mstart the proxy\033[0m\n'
        printf '        \033[1m$\033[0m frugal serve\n'
        echo
        printf '  \033[1;32m2.\033[0m  \033[1mpoint your app at it\033[0m  \033[2m(any OpenAI-compatible SDK)\033[0m\n'
        printf '        \033[1m$\033[0m export OPENAI_BASE_URL=http://localhost:8080/v1\n'
        echo
        printf '  \033[1;32m3.\033[0m  \033[1mroute by use case\033[0m  \033[2m— the toolchain bundle that wins on the bench\033[0m\n'
        printf '        \033[1m$\033[0m curl "$OPENAI_BASE_URL/chat/completions" \\\n'
        printf '            -H "X-Frugal-Use-Case: research-synthesis" \\\n'
        printf '            -d '\''{"model":"auto","messages":[{"role":"user","content":"hi"}]}'\''\n'
        echo
        printf '  \033[1;32m4.\033[0m  \033[1msee what frugal saved\033[0m\n'
        printf '        \033[1m$\033[0m frugal bench --out BENCHMARKS.md\n'
        printf '        \033[2mor read the live run →\033[0m \033[1;36mhttps://frugal.sh/benchmark\033[0m\n'
    fi

    echo
    printf '  \033[2muninstall:  curl -fsSL https://frugal.sh/install | bash -s uninstall\033[0m\n'
    echo
    printf '\033[2m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m\n'
    echo
}

main "$@"
