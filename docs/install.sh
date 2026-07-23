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

resolve_version_via_redirect() {
    # Quota-free fallback: github.com/<repo>/releases/latest 302s to
    # .../releases/tag/<tag>. Follow the redirect and read the final URL.
    local latest_url="https://github.com/${REPO}/releases/latest" final=""
    if command -v curl >/dev/null 2>&1; then
        final="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$latest_url")" || return 1
    elif command -v wget >/dev/null 2>&1; then
        # wget -S prints the redirect chain to stderr; the last Location wins.
        final="$(wget --max-redirect=5 -qO /dev/null -S "$latest_url" 2>&1 \
            | sed -nE 's/^[[:space:]]*Location: *([^[:space:]]+).*/\1/p' | tail -n1)" || return 1
    else
        return 1
    fi
    case "$final" in
        */releases/tag/*) printf '%s\n' "${final##*/releases/tag/}" ;;
        *) return 1 ;;
    esac
}

resolve_version() {
    if [ -n "$PINNED_VERSION" ]; then
        echo "$PINNED_VERSION"
        return
    fi
    local api_url="https://api.github.com/repos/${REPO}/releases/latest"
    local json="" tag=""
    # Unauthenticated GitHub API requests are rate-limited to 60/hour per IP.
    # Shared CI runner pools and NATed offices blow that cap easily. Honour
    # GITHUB_TOKEN when present (bumps the cap to 5000/hour) — silent no-op
    # for end users. API failure is non-fatal here: the redirect fallback
    # below spends no API quota at all.
    if command -v curl >/dev/null 2>&1; then
        if [ -n "${GITHUB_TOKEN:-}" ]; then
            json="$(curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "$api_url" || true)"
        else
            json="$(curl -fsSL "$api_url" || true)"
        fi
    elif command -v wget >/dev/null 2>&1; then
        json="$(wget -qO- "$api_url" || true)"
    else
        fail "curl or wget is required" "$EXIT_NETWORK"
    fi
    if [ -n "$json" ]; then
        if command -v jq >/dev/null 2>&1; then
            tag="$(printf '%s' "$json" | jq -r '.tag_name // empty')"
        else
            # Strict anchored regex; yields empty if the JSON shape shifts so
            # we never silently install the wrong version.
            tag="$(printf '%s' "$json" | sed -nE 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' | head -n1)"
        fi
    fi
    if [ -z "$tag" ]; then
        tag="$(resolve_version_via_redirect || true)"
    fi
    [ -n "$tag" ] || fail "could not resolve latest version (GitHub API and releases/latest redirect both failed)" "$EXIT_NETWORK"
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

    # Agent-client configs are left untouched on purpose: this script never
    # edits files owned by other apps on a destructive path. Any frugal entry
    # `frugal mcp install` wrote there now points at a deleted binary, so
    # tell the user exactly what was left and how to remove each one.
    local desktop_cfg cursor_cfg
    case "$(uname -s)" in
        Darwin) desktop_cfg="$HOME/Library/Application Support/Claude/claude_desktop_config.json" ;;
        *)      desktop_cfg="$HOME/.config/Claude/claude_desktop_config.json" ;;
    esac
    cursor_cfg="$HOME/.cursor/mcp.json"
    if [ -f "$desktop_cfg" ] && grep -q '"frugal"' "$desktop_cfg"; then
        warn "left in place: Claude Desktop still lists frugal (now pointing at a deleted binary)"
        echo "    remove the \"frugal\" entry under mcpServers in: $desktop_cfg"
    fi
    if [ -f "$cursor_cfg" ] && grep -q '"frugal"' "$cursor_cfg"; then
        warn "left in place: Cursor still lists frugal (now pointing at a deleted binary)"
        echo "    remove the \"frugal\" entry under mcpServers in: $cursor_cfg"
    fi
    if command -v claude >/dev/null 2>&1; then
        warn "left in place: Claude Code may still list frugal"
        echo "    claude mcp remove frugal"
    fi

    echo
    echo "frugal.sh uninstalled."
    exit 0
}

# ---- Install ----

main() {
    if [ "${1:-}" = "uninstall" ]; then
        uninstall
    fi

    info "installing frugal.sh — the open routing layer for AI tools"
    info "tool calls routed per policy, decision on every result. any model. BYOK."
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

    # Search provider detection — informational only. Marginalia ships
    # always-on (public API, no key, no URL config), so even a fresh
    # install has at least one search provider available out of the box.
    echo
    info "detecting search-provider config..."
    local configured=0
    # Marginalia is always available in the default models.yaml — no
    # env var to detect, just announce it.
    ok "Marginalia (free, public — always on)"
    configured=$((configured + 1))
    [ -n "${SEARXNG_URL:-}" ]    && { ok "SearXNG (\$SEARXNG_URL set)";    configured=$((configured + 1)); }
    [ -n "${SERPER_API_KEY:-}" ] && { ok "Serper (\$SERPER_API_KEY set)";  configured=$((configured + 1)); }
    [ -n "${YDC_API_KEY:-}" ]    && { ok "You.com (\$YDC_API_KEY set)";    configured=$((configured + 1)); }
    echo
    printf '\033[2m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m\n'
    echo
    printf '  \033[1;32m✓\033[0m  \033[1mfrugal.sh installed\033[0m  \033[2m·  %s  ·  %s\033[0m\n' "$version" "$platform"
    echo

    # Show the "add more providers" panel unless the user has at least
    # one paid tier configured. Marginalia alone works — but a paid
    # fallback is the honest recommendation for production use.
    if [ -z "${SERPER_API_KEY:-}" ] && [ -z "${YDC_API_KEY:-}" ] && [ -z "${SEARXNG_URL:-}" ]; then
        printf '  \033[1;33m⚠\033[0m  Only Marginalia is wired — fine for casual use, but consider adding\n'
        printf '     a paid fallback for production. Frugal has no SaaS or account.\n'
        echo
        printf '  \033[2m─── \033[0m\033[1;36mAdd a provider\033[0m\033[2m ──────────────────────────────────────\033[0m\n'
        echo
        # The "$0"/"$0.001" below are literal dollar prices, not expansions.
        # shellcheck disable=SC2016
        printf '  \033[1;32m▸\033[0m  SearXNG    \033[2m($0 — self-hosted instance URL, not a key)\033[0m\n'
        printf '        \033[1m$\033[0m export SEARXNG_URL=https://your-searxng-instance/\n'
        echo
        # shellcheck disable=SC2016
        printf '  \033[1;32m▸\033[0m  Serper     \033[2m($0.001/call list · 2,500 free credits on signup, no card)\033[0m\n'
        printf '        \033[1m$\033[0m export SERPER_API_KEY=...   \033[2m(serper.dev)\033[0m\n'
        echo
        # shellcheck disable=SC2016
        printf '  \033[1;32m▸\033[0m  You.com    \033[2m($0.005/call list · $100 free credit on signup ~ 20k calls)\033[0m\n'
        printf '        \033[1m$\033[0m export YDC_API_KEY=...      \033[2m(you.com/platform)\033[0m\n'
        echo
        printf '  \033[2m   You.com also offers an MCP server with 100 free queries/day, no\n'
        printf '     key required — agents can hit api.you.com/mcp?profile=free directly.\n'
        printf '     Useful path if you want a no-signup You.com option outside Frugal.\033[0m\n'
        echo
        # Absolute path on purpose: the PATH export landed in the rc file,
        # not in the shell the user is pasting into — bare `frugal` would
        # not resolve until they open a new shell.
        printf '  Then wire frugal into your agent:\n'
        printf '        \033[1m$\033[0m %s mcp install\n' "$BIN_DIR/frugal"
        printf '        \033[2m(plain "frugal" works once you open a new shell)\033[0m\n'
    else
        printf '  \033[2m─── \033[0m\033[1;36mWire it in\033[0m\033[2m ──────────────────────────────────────\033[0m\n'
        echo
        printf '  \033[1;32m1.\033[0m  \033[1minstall into your agent stack\033[0m  \033[2m(Claude Desktop / Cursor / Claude Code)\033[0m\n'
        printf '        \033[1m$\033[0m %s mcp install\n' "$BIN_DIR/frugal"
        printf '        \033[2m(plain "frugal" works once you open a new shell)\033[0m\n'
        echo
        printf '  \033[1;32m2.\033[0m  \033[1mrestart your agent\033[0m  \033[2m(the frugal__search tool appears in its tool picker)\033[0m\n'
    fi

    echo
    printf '  \033[2muninstall:  curl -fsSL https://frugal.sh/install | bash -s uninstall\033[0m\n'
    echo
    printf '\033[2m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m\n'
    echo
}

main "$@"
