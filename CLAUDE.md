# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Ghostline is an AI-powered zsh terminal assistant that runs as a local background daemon. It provides two features:
1. **Inline command completion** — `Ctrl+Space` completes the current command buffer using an LLM
2. **Error recovery** — after a non-zero exit, it automatically suggests a fix printed below the error

## Inference backend

Inference is **pluggable**, selected by `backend:` in config (see below):

- **`anthropic` (current default)** — the Claude API. Default model is `claude-haiku-4-5-20251001` (fast/cheap, keeps inline completion latency low). The API key is read from the `ANTHROPIC_API_KEY` environment variable (or `anthropic_api_key` in config). It is **never** hardcoded, committed, or logged.
- **`ollama` (local option)** — fully local inference via [Ollama](https://ollama.com), default model `qwen2.5-coder:3b`. Zero-network path; select with `backend: ollama`.

Both backends implement the single `Generator` interface (`Generate` + `Ping`) in `internal/llm`, so completion and recovery are backend-agnostic.

## Build and install

```bash
# Build the binary
make build

# Build + install to /usr/local/bin + copy shell integration to ~/.ghostline/
make install

# Verify prerequisites (API key set, or Ollama running + model downloaded)
make setup   # or: ./ghostline setup

# Remove completely (stops daemon, removes binary and ~/.ghostline/)
make uninstall

# Delete the binary artifact
make clean
```

After `make install`, run `source ~/.zshrc` for the shell integration to activate.

**macOS install note:** `make install` copies the binary with `cp`. If the copy writes to corrupt disk blocks (rare but observed), new invocations will wedge in uninterruptible sleep (`UE` STAT) while the previously-loaded daemon continues running. Fix: `sudo rm /usr/local/bin/ghostline && sudo cp ghostline /usr/local/bin/ghostline` to force fresh block allocation, then restart the daemon.

## Running tests

Unit tests cover the two brittle parsers (`sanitize()` and `parseResponse()` / `tryDeterministic()`):

```bash
go build ./...
go vet ./...
go test ./...
```

## Architecture

```
cmd/ghostline/main.go        — CLI entry point (cobra); all subcommands defined here
internal/daemon/daemon.go    — Unix socket server + client helpers (EnsureDaemon, SendRequest)
internal/config/config.go    — Config struct, YAML loader, path helpers (~/.ghostline/)
internal/session/store.go    — In-memory per-session context (recent commands, cwd, git info)
internal/completion/         — Completion prompt builder + suffix sanitizer
internal/recovery/           — Error recovery: deterministic typo corrector + LLM prompt/parser
internal/llm/llm.go          — Backend interface + factory (selects anthropic or ollama)
internal/anthropic/client.go — HTTP client for the Claude API /v1/messages endpoint
internal/ollama/client.go    — HTTP client for Ollama's /api/generate endpoint
internal/context/detector.go — Git branch/repo detection + project type detection
shell/ghostline.zsh          — Zsh integration (ZLE widgets, preexec/precmd hooks)
```

### Request flow

The shell integration (`ghostline.zsh`) communicates with the Go binary exclusively through CLI subcommands. The binary communicates with the daemon over a Unix domain socket at `~/.ghostline/ghostline.sock`.

- `Ctrl+Space` → zsh calls `ghostline complete --buffer <text> --session <id>` → binary sends `{"type":"complete"}` to daemon → daemon calls the backend → returns the full completed command to stdout. The zsh widget writes that string straight into `BUFFER` and moves the cursor to the end.
- After failed command → zsh calls `ghostline recover --cmd ... --stderr ...` → `{"type":"recover"}` → daemon first runs a fast **deterministic** corrector (single-token mistyped command); only on a miss does it call the backend → returns the runnable fix on stdout line 1 and the WHY on line 2. The zsh `precmd` prints the WHY as an explanation and uses `print -z` to **pre-fill the corrected command into the next prompt**, so the user just presses Enter.
- After every command → zsh fires `ghostline context update` (async, fire-and-forget) → `{"type":"update_context"}` → daemon detects git/project info and updates session store.

### Context passed to the LLM

Each request includes session context built up over the shell's lifetime:
- Current working directory
- Git branch and remote repo name (detected via `git -C <cwd> branch --show-current` and `git remote get-url origin`)
- Project type — detected from marker files: `go.mod` → go, `package.json` → node, `requirements.txt`/`pyproject.toml` → python, `Cargo.toml` → rust, `pom.xml`/`build.gradle` → java, `Gemfile` → ruby, `*.tf` → terraform
- Last 5 recent commands (from a rolling window of up to `max_context_commands`)

### Config file

`~/.ghostline/config.yaml` (created manually; all fields optional):

```yaml
backend: anthropic                      # "anthropic" (default) or "ollama"
anthropic_model: claude-haiku-4-5-20251001
anthropic_api_key: ""                   # optional; prefer the ANTHROPIC_API_KEY env var
model: qwen2.5-coder:3b                 # ollama model (used when backend: ollama)
ollama_host: http://localhost:11434
completion_timeout_ms: 5000
recovery_timeout_ms: 10000
max_context_commands: 15
```

The Claude API key is resolved from `anthropic_api_key` if set, otherwise from `ANTHROPIC_API_KEY` in the environment. Keep it in the env var; never commit it.

### Daemon lifecycle

- Auto-started by `ghostline.zsh` on shell init if not running (`ghostline server --background`)
- `EnsureDaemon()` in `daemon.go` also auto-starts it silently before any `complete` or `recover` call
- PID written to `~/.ghostline/ghostline.pid`; stale socket removed on startup
- Sessions are evicted from memory after 24 hours of inactivity (hourly eviction loop in `store.go`)
- Check status: `ghostline status` returns JSON with `status`, `model`, and active session count

### LLM prompt contracts

**Completion** (`internal/completion/prompt.go` + `completer.go`):
- Prompt instructs the model to return ONLY the suffix (text after the buffer). Example: input `git ch` → model returns `eckout`.
- `sanitize()` strips code fences/quotes/whitespace and defensively handles models that echo the whole command back, converting to a pure suffix before concatenating `buffer + suffix`.
- Max tokens: 80. Timeout: `completion_timeout_ms` (default 5s).

**Recovery** (`internal/recovery/correct.go` + `recovery.go`):
- Two tiers:
  1. **Deterministic** (`tryDeterministic()`): fires only on exit 127 / "command not found". Handles single-token commands only (no args). Uses a hardcoded `knownTypos` map for high-frequency misspellings (`gti`→`git`, `dokcer`→`docker`, etc.) and edit-distance-1 matching against a `commonCommands` allow-list. Returns a unique match or nothing (ambiguous = no suggestion).
  2. **LLM**: called when deterministic returns nil — handles multi-token lines where args may also be wrong (e.g. `gitt stauts` → `git status`). Model returns `FIX: <runnable command>` + `WHY: <short clause>` or the literal `NONE`.
- `parseResponse()` also defensively splits an inlined `command — explanation` on any Unicode dash (em/en/figure/horizontal-bar/minus-sign) so the FIX stays runnable — critical because the shell pre-fills it into the prompt buffer.
- Max tokens: 120. Timeout: `recovery_timeout_ms` (default 10s).
