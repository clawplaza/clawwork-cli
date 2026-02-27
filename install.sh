#!/usr/bin/env bash
# ClawWork CLI installer
# Usage: curl -fsSL https://dl.clawplaza.ai/clawwork/install.sh | bash
#
# Environment variables:
#   INSTALL_DIR  — override install directory (default: ~/.clawwork/bin)
#   VERSION      — install a specific version (default: latest)

set -euo pipefail

CDN_BASE="https://dl.clawplaza.ai/clawwork"
DEFAULT_INSTALL_DIR="$HOME/.clawwork/bin"
BINARY_NAME="clawwork"

# --- helpers ---

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m==>\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m==>\033[0m %s\n" "$*"; }
error() { printf "\033[1;31m==>\033[0m %s\n" "$*" >&2; exit 1; }

need_cmd() {
    if ! command -v "$1" &>/dev/null; then
        error "Required command not found: $1"
    fi
}

# --- detect platform ---

detect_os() {
    local os
    os="$(uname -s)"
    case "$os" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        *)       error "Unsupported OS: $os (only Linux and macOS are supported)" ;;
    esac
}

detect_arch() {
    local arch
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64)  echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *)             error "Unsupported architecture: $arch" ;;
    esac
}

# --- fetch latest version ---

fetch_version() {
    if [ -n "${VERSION:-}" ]; then
        echo "$VERSION"
        return
    fi
    local ver
    ver="$(curl -fsSL "$CDN_BASE/version.json" 2>/dev/null)" || error "Failed to fetch version info from CDN"
    echo "$ver" | grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | cut -d'"' -f4
}

# --- main ---

main() {
    need_cmd curl
    need_cmd tar
    need_cmd uname

    local os arch version install_dir archive_url archive_file

    os="$(detect_os)"
    arch="$(detect_arch)"

    info "Detected platform: ${os}/${arch}"

    version="$(fetch_version)"
    [ -z "$version" ] && error "Could not determine latest version"
    info "Installing ClawWork CLI v${version}"

    install_dir="${INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"
    mkdir -p "$install_dir"

    # Build download URL (matches GoReleaser name_template)
    archive_url="${CDN_BASE}/v${version}/${BINARY_NAME}_${version}_${os}_${arch}.tar.gz"

    # Download
    archive_file="$(mktemp)"
    info "Downloading ${archive_url}"
    curl -fSL --progress-bar "$archive_url" -o "$archive_file" || error "Download failed. Check your network or try VERSION=x.y.z"

    # Extract binary
    info "Extracting..."
    tar xzf "$archive_file" -C "$install_dir" "$BINARY_NAME" 2>/dev/null \
        || tar xzf "$archive_file" -C "$install_dir" --strip-components=1 "$BINARY_NAME" 2>/dev/null \
        || {
            # fallback: extract everything and pick the binary
            local tmp_dir
            tmp_dir="$(mktemp -d)"
            tar xzf "$archive_file" -C "$tmp_dir"
            local found
            found="$(find "$tmp_dir" -name "$BINARY_NAME" -type f | head -1)"
            [ -z "$found" ] && error "Binary not found in archive"
            mv "$found" "$install_dir/$BINARY_NAME"
            rm -rf "$tmp_dir"
        }

    chmod +x "$install_dir/$BINARY_NAME"
    rm -f "$archive_file"

    ok "Installed: ${install_dir}/${BINARY_NAME}"

    # Check PATH
    local needs_path=false
    case ":$PATH:" in
        *":${install_dir}:"*) ;;
        *) needs_path=true ;;
    esac

    if $needs_path; then
        warn "${install_dir} is not in your PATH"
        echo ""
        echo "  Add it to your shell profile:"
        echo ""

        local shell_name
        shell_name="$(basename "${SHELL:-/bin/bash}")"
        local profile
        case "$shell_name" in
            zsh)  profile="~/.zshrc" ;;
            bash)
                if [ -f "$HOME/.bash_profile" ]; then
                    profile="~/.bash_profile"
                else
                    profile="~/.bashrc"
                fi
                ;;
            fish) profile="~/.config/fish/config.fish" ;;
            *)    profile="~/.profile" ;;
        esac

        if [ "$shell_name" = "fish" ]; then
            echo "    echo 'set -gx PATH ${install_dir} \$PATH' >> ${profile}"
        else
            echo "    echo 'export PATH=\"${install_dir}:\$PATH\"' >> ${profile}"
        fi
        echo ""
        echo "  Then restart your terminal or run:"
        echo ""
        echo "    export PATH=\"${install_dir}:\$PATH\""
        echo ""
    fi

    # Verify
    if command -v "$BINARY_NAME" &>/dev/null || [ -x "$install_dir/$BINARY_NAME" ]; then
        ok "Run 'clawwork version' to verify, then 'clawwork init' to get started."
    fi
}

main "$@"
