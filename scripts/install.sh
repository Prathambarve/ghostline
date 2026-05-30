#!/usr/bin/env bash
# ghostline install script
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Prathambarve/ghostline/main/scripts/install.sh | bash
#   or from a cloned repo: bash scripts/install.sh
set -euo pipefail

REPO="Prathambarve/ghostline"
VERSION="${GHOSTLINE_VERSION:-latest}"
DOT_DIR="$HOME/.ghostline"

# ── Colours ───────────────────────────────────────────────────────────────────
if [[ -t 1 ]]; then
    GREEN='\033[0;32m'; YELLOW='\033[0;33m'; RED='\033[0;31m'; RESET='\033[0m'; BOLD='\033[1m'
else
    GREEN=''; YELLOW=''; RED=''; RESET=''; BOLD=''
fi
info()  { printf "${GREEN}✓${RESET}  %s\n" "$*"; }
warn()  { printf "${YELLOW}!${RESET}  %s\n" "$*"; }
err()   { printf "${RED}✗${RESET}  %s\n" "$*" >&2; }
step()  { printf "${BOLD}→${RESET}  %s\n" "$*"; }

# ── Detect OS and architecture ────────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        err "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac
case "$OS" in
    linux|darwin) ;;
    *)
        err "Unsupported OS: $OS"
        exit 1
        ;;
esac

BINARY_ASSET="ghostline-${OS}-${ARCH}"
step "Detected ${OS}/${ARCH}"

# ── Find or download the binary ───────────────────────────────────────────────
TMP_BIN=""

# Running from a cloned repo — use the locally built binary if available.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo ".")"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
LOCAL_BIN=""

if [[ -f "$REPO_ROOT/${BINARY_ASSET}" ]]; then
    LOCAL_BIN="$REPO_ROOT/${BINARY_ASSET}"
elif [[ -f "$REPO_ROOT/ghostline" ]]; then
    LOCAL_BIN="$REPO_ROOT/ghostline"
fi

if [[ -n "$LOCAL_BIN" ]]; then
    step "Using local binary: $LOCAL_BIN"
    SRC="$LOCAL_BIN"
else
    # Resolve "latest" to a real tag via GitHub API.
    if [[ "$VERSION" == "latest" ]]; then
        if command -v curl &>/dev/null; then
            VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
                | grep '"tag_name"' | head -1 | cut -d'"' -f4)"
        elif command -v wget &>/dev/null; then
            VERSION="$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" \
                | grep '"tag_name"' | head -1 | cut -d'"' -f4)"
        fi
        [[ -z "$VERSION" ]] && { err "Could not resolve latest release version."; exit 1; }
    fi

    URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY_ASSET}"
    step "Downloading ghostline ${VERSION}..."

    TMP_BIN="$(mktemp)"
    if command -v curl &>/dev/null; then
        curl -fsSL --progress-bar "$URL" -o "$TMP_BIN"
    elif command -v wget &>/dev/null; then
        wget -q --show-progress "$URL" -O "$TMP_BIN" 2>&1 || wget -q "$URL" -O "$TMP_BIN"
    else
        err "Neither curl nor wget found. Install one and retry."
        exit 1
    fi
    SRC="$TMP_BIN"
fi

# ── Choose install directory ──────────────────────────────────────────────────
# Prefer ~/.local/bin (no root needed); fall back to /usr/local/bin with sudo.
if [[ -n "${GHOSTLINE_INSTALL_DIR:-}" ]]; then
    INSTALL_DIR="$GHOSTLINE_INSTALL_DIR"
elif [[ ":$PATH:" == *":$HOME/.local/bin:"* ]] || [[ -d "$HOME/.local/bin" ]]; then
    INSTALL_DIR="$HOME/.local/bin"
elif [[ -w "/usr/local/bin" ]]; then
    INSTALL_DIR="/usr/local/bin"
else
    INSTALL_DIR="$HOME/.local/bin"
fi

mkdir -p "$INSTALL_DIR"
DEST="${INSTALL_DIR}/ghostline"

step "Installing to ${DEST}..."
if [[ -w "$INSTALL_DIR" ]]; then
    install -m 755 "$SRC" "$DEST"
else
    sudo install -m 755 "$SRC" "$DEST"
fi
[[ -n "$TMP_BIN" ]] && rm -f "$TMP_BIN"
info "Binary installed"

# Ensure install dir is in PATH for this session and print a hint if not.
export PATH="${INSTALL_DIR}:${PATH}"
if ! command -v ghostline &>/dev/null; then
    warn "${INSTALL_DIR} is not in your PATH."
    warn "Add this to your shell rc file:"
    warn "  export PATH=\"${INSTALL_DIR}:\$PATH\""
fi

# ── Install shell integration ─────────────────────────────────────────────────
step "Installing shell integration to ${DOT_DIR}..."
mkdir -p "$DOT_DIR"

copy_integration() {
    local name="$1"
    local dest="${DOT_DIR}/${name}"
    # Local repo copy
    if [[ -f "${REPO_ROOT}/shell/${name}" ]]; then
        cp "${REPO_ROOT}/shell/${name}" "$dest"
        return
    fi
    # Download from GitHub
    local raw_url="https://raw.githubusercontent.com/${REPO}/main/shell/${name}"
    if command -v curl &>/dev/null; then
        curl -fsSL "$raw_url" -o "$dest"
    elif command -v wget &>/dev/null; then
        wget -qO "$dest" "$raw_url"
    fi
}

copy_integration "ghostline.zsh"
copy_integration "ghostline.bash"
info "Shell integrations installed"

# ── Wire up shell rc file ─────────────────────────────────────────────────────
SHELL_NAME="$(basename "${SHELL:-/bin/bash}")"

wire_rc() {
    local rc="$1"
    local src_line="$2"
    local path_line="${3:-}"

    [[ ! -f "$rc" ]] && touch "$rc"

    if grep -q 'ghostline' "$rc" 2>/dev/null; then
        return 0  # already present
    fi

    {
        printf '\n# ghostline AI terminal assistant\n'
        [[ -n "$path_line" ]] && printf '%s\n' "$path_line"
        printf '%s\n' "$src_line"
    } >> "$rc"
    info "Added to ${rc}"
}

PATH_LINE=""
if [[ "$INSTALL_DIR" == "$HOME/.local/bin" ]]; then
    PATH_LINE='export PATH="$HOME/.local/bin:$PATH"'
fi

case "$SHELL_NAME" in
    zsh)
        wire_rc "$HOME/.zshrc" 'source ~/.ghostline/ghostline.zsh' "$PATH_LINE"
        ;;
    bash)
        wire_rc "$HOME/.bashrc" 'source ~/.ghostline/ghostline.bash' "$PATH_LINE"
        # Login shells on macOS source .bash_profile not .bashrc
        if [[ "$OS" == "darwin" ]] && [[ -f "$HOME/.bash_profile" ]]; then
            if ! grep -q 'ghostline\|\.bashrc' "$HOME/.bash_profile" 2>/dev/null; then
                printf '\n[[ -f ~/.bashrc ]] && source ~/.bashrc\n' >> "$HOME/.bash_profile"
            fi
        fi
        ;;
    *)
        warn "Unknown shell: ${SHELL_NAME}"
        warn "Manually add to your rc file:"
        warn "  source ~/.ghostline/ghostline.bash   # for bash"
        warn "  source ~/.ghostline/ghostline.zsh    # for zsh"
        ;;
esac

# ── API key setup ─────────────────────────────────────────────────────────────
printf '\n'
printf "${BOLD}ghostline installed!${RESET}\n"
printf '\n'

if [[ -z "${ANTHROPIC_API_KEY:-}" && -z "${OPENAI_API_KEY:-}" && -z "${GROQ_API_KEY:-}" ]]; then
    printf "Set your API key — add one of these to your shell rc file:\n\n"
    printf "  export ANTHROPIC_API_KEY=your-key   # Claude (recommended)\n"
    printf "  export OPENAI_API_KEY=your-key       # OpenAI\n"
    printf "  export GROQ_API_KEY=your-key         # Groq (free tier)\n"
    printf '\n'
    printf "Then reload and verify:\n"
    printf "  source ~/.${SHELL_NAME}rc\n"
    printf "  ghostline setup\n"
else
    printf "API key detected. Reload your shell to activate:\n\n"
    printf "  source ~/.${SHELL_NAME}rc\n"
    printf "  ghostline setup\n"
fi
printf '\n'
