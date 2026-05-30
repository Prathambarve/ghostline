#!/usr/bin/env bash
set -euo pipefail

BINARY=ghostline
INSTALL=/usr/local/bin/$BINARY
DOT_DIR="$HOME/.ghostline"
MODEL="qwen2.5-coder:3b"
OLLAMA_URL="http://localhost:11434"

log()  { printf '  \033[1;32m✓\033[0m  %s\n' "$*"; }
info() { printf '  \033[1;34m→\033[0m  %s\n' "$*"; }
fail() { printf '\n  \033[1;31m✗\033[0m  %s\n\n' "$*" >&2; exit 1; }

echo ""
echo "  ghostline installer"
echo "  ───────────────────"
echo ""

# ── Prerequisites ─────────────────────────────────────────────────────────────

command -v go &>/dev/null    || fail "Go is required. Install from https://go.dev/dl/"
command -v brew &>/dev/null  || fail "Homebrew is required. Install from https://brew.sh"
log "Prerequisites OK"

# ── Ollama ────────────────────────────────────────────────────────────────────

if ! command -v ollama &>/dev/null; then
    info "Installing Ollama..."
    brew install ollama
fi
log "Ollama installed"

# Start if not already responding
if ! curl -sf "$OLLAMA_URL/api/tags" &>/dev/null; then
    info "Starting Ollama..."
    brew services start ollama &>/dev/null
    for _ in $(seq 1 20); do
        curl -sf "$OLLAMA_URL/api/tags" &>/dev/null && break
        sleep 0.5
    done
    curl -sf "$OLLAMA_URL/api/tags" &>/dev/null \
        || fail "Ollama did not start. Try running: ollama serve"
fi
log "Ollama running"

# ── Build & install binary ────────────────────────────────────────────────────

info "Building ghostline..."
go build -o "$BINARY" ./cmd/ghostline
log "Built"

if ! cp "$BINARY" "$INSTALL" 2>/dev/null; then
    sudo cp "$BINARY" "$INSTALL" || fail "Could not install to $INSTALL"
fi
log "Installed to $INSTALL"

# ── Shell integration ─────────────────────────────────────────────────────────

mkdir -p "$DOT_DIR"
cp shell/ghostline.zsh "$DOT_DIR/ghostline.zsh"
log "Shell integration ready"

if ! grep -q 'ghostline.zsh' "$HOME/.zshrc" 2>/dev/null; then
    printf '\n# ghostline AI terminal assistant\nsource ~/.ghostline/ghostline.zsh\n' >> "$HOME/.zshrc"
    log "Added to ~/.zshrc"
else
    log "~/.zshrc already configured"
fi

# ── Pull model in background ──────────────────────────────────────────────────

if ollama list 2>/dev/null | grep -q "^${MODEL}"; then
    log "Model ${MODEL} already downloaded"
else
    info "Downloading model ${MODEL} in background (~2 GB)..."
    nohup ollama pull "$MODEL" > "$DOT_DIR/model-pull.log" 2>&1 &
    echo ""
    echo "  The AI model is downloading in the background."
    echo "  Track progress:  tail -f ~/.ghostline/model-pull.log"
    echo "  ghostline will activate automatically once the download finishes."
fi

# ─────────────────────────────────────────────────────────────────────────────

echo ""
echo "  Done! Open a new terminal, or run:  source ~/.zshrc"
echo ""
echo "  Usage:"
echo "    Ctrl+Space   — complete the current command"
echo "    Right arrow  — accept the suggestion"
echo "    (errors)     — fix suggestions appear automatically"
echo ""
