# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Ghostline is an AI-powered zsh terminal assistant that runs as a local background daemon. It provides two features:
1. **Inline command completion** — `Ctrl+Space` completes the current command buffer using an LLM
2. **Error recovery** — after a non-zero exit, it automatically suggests a fix printed below the error

## Inference backend

Inference is **pluggable**, selected by `backend:` in config (see below):

- **`anthropic` (current default)** — the Claude API. Default model is `claude-haiku-4-5-20251001` (fast/cheap, keeps inline completion latency low). The API key is read from the `ANTHROPIC_API_KEY` environment variable (or `anthropic_api_key` in config). It is **never** hardcoded, committed, or logged.
- **`openai` (alternate cloud)** — the OpenAI Chat Completions API, default model `gpt-4o-mini`. Key is read from the `OPENAI_API_KEY` environment variable (or `openai_api_key` in config). Select with `backend: openai`.
- **`groq` (fast cloud, free tier)** — Groq's OpenAI-compatible endpoint, default model `llama-3.3-70b-versatile`. Extremely low latency (good for the inline path); key from `GROQ_API_KEY` (or `groq_api_key`). Select with `backend: groq`. **Implemented by reusing the `openai` client** via `openai.NewCompatible(...)` pointed at `https://api.groq.com/openai/v1/chat/completions` — no separate package.

All backends implement the single `Generator` interface (`Generate` + `Ping`) in `internal/llm`, so completion and recovery are backend-agnostic.

## Build and install

```bash
# Build the binary
make build

# Build + install to /usr/local/bin + copy shell integration to ~/.ghostline/
make install

# Verify prerequisites (API key set for the selected backend)
make setup   # or: ./ghostline setup

# Remove completely (stops daemon, removes binary and ~/.ghostline/)
make uninstall

# Delete the binary artifact
make clean
```

On first run, `ghostline setup` is an interactive transparency/consent wizard. It asks the user which backend to use and what terminal context Ghostline may store or send to that backend. Pressing Enter picks the Recommended option. The daemon will not auto-start until setup has been completed. After `make install`, run `source ~/.zshrc` for the shell integration to activate.

**macOS install note:** `make install` uses `install(1)` which writes to a temp file and renames atomically, always allocating fresh disk blocks. This prevents the macOS UE-sleep bug that occurred with plain `cp` (in-place overwrite could reuse flaky disk blocks, wedging new processes in uninterruptible sleep).

## Running tests

Unit tests cover the two brittle parsers (`sanitize()` and `parseResponse()` / `tryDeterministic()`):

```bash
go build ./...
go vet ./...
go test ./...
```

## Architecture

```
cmd/ghostline/main.go        — CLI entry point (cobra); subcommands defined here
cmd/ghostline/bench.go       — `ghostline bench` latency benchmark across backends
internal/daemon/daemon.go    — Unix socket server + client helpers (EnsureDaemon, SendRequest)
internal/config/config.go    — Config struct, YAML loader, path helpers (~/.ghostline/)
internal/session/store.go    — In-memory per-session context (recent commands, cwd, git info)
internal/history/            — Persistent, redacted JSONL command log for cross-session suggestions
internal/fixcache/           — Learned error→fix cache: replays user-accepted recoveries offline (no API call)
internal/secret/             — Value-based secret detector (provider key prefixes, secret assignments); backs the prompt guard + history denylist
internal/workflow/           — User-authored saved commands (YAML), surfaced in the command palette
internal/completion/menu.go  — Multi-candidate described completions (Candidate type) behind the Fig-style picker
internal/completion/         — Completion prompt builder + suffix sanitizer + intent-mode prompt
internal/recovery/           — Error recovery: deterministic typo corrector + LLM prompt/parser
internal/llm/llm.go          — Backend interface + factory (selects anthropic, openai, or groq)
internal/anthropic/client.go — HTTP client for the Claude API /v1/messages endpoint
internal/openai/client.go    — HTTP client for OpenAI-compatible /chat/completions (OpenAI + Groq via NewCompatible)
internal/context/detector.go — Git branch/repo detection + project type detection
shell/ghostline.zsh          — Zsh integration (ZLE widgets, preexec/precmd hooks) — FULL feature set
shell/ghostline.bash         — Bash integration (readline bind -x, DEBUG trap, PROMPT_COMMAND) — BASELINE only (see parity note)
```

### Shell integration parity (zsh vs bash)

The Go binary is the single source of truth and is fully cross-platform (Linux/macOS, any terminal); every feature is reachable through CLI subcommands. The **shell glue is what differs**, and the two front-ends are NOT at parity:

| Feature | `ghostline.zsh` | `ghostline.bash` |
|---|---|---|
| Inline completion (`Ctrl+Space`) | ✅ | ✅ |
| Error recovery (pre-fill fix) | ✅ | ✅ |
| Secret guard at the prompt (Phase 3) | ✅ | ❌ not ported |
| Completion menu — Fig-style (Phase 4) | ✅ | ❌ not ported |
| Command palette (Phase 4) | ✅ | ❌ not ported |
| Workflow keybinding (Phase 4) | ✅ | ❌ (CLI `ghostline workflow …` works everywhere; only the keybinding is missing) |

**Why it's not a copy-paste port:** the zsh integration is ZLE-based (`zle -N`, `BUFFER`/`CURSOR`, `zle -M`, the `accept-line` override, `${1:l}`, `print -z`, 1-indexed arrays, the `<->` glob). Bash uses readline instead (`bind -x` with `READLINE_LINE`/`READLINE_POINT`), has no ZLE, no native preexec (DEBUG trap), and 0-indexed arrays. Each feature needs a bash reimplementation. Specifically:
- **Palette + completion menu** port cleanly via `bind -x` rewriting `READLINE_LINE`; the `_ghostline_pick` helper is mostly portable (drop the `<->` glob and 1-indexed arrays).
- **Secret guard** is the hard one: bash readline has no clean equivalent of overriding `accept-line` to "swallow Enter, warn, run on the second Enter." A bash port likely has to bind Return via `bind -x` and re-issue the command, which is fiddly and can conflict with other readline setups.

When adding a new shell-facing feature, implement it in `ghostline.zsh` AND `ghostline.bash` (or explicitly record the gap here). New keybindings should use plain control-byte chords like `^X^N` (every terminal transmits these) rather than `Ctrl+Space`/NUL, which several terminals (macOS Terminal.app, default iTerm2) do not send.

### Request flow

The shell integration (`ghostline.zsh`) communicates with the Go binary exclusively through CLI subcommands. The binary communicates with the daemon over a Unix domain socket at `~/.ghostline/ghostline.sock`.

- `Ctrl+Space` → zsh calls `ghostline complete --buffer <text> --session <id>` → binary sends `{"type":"complete"}` to daemon → daemon calls the backend → returns the full completed command to stdout. The zsh widget writes that string straight into `BUFFER` and moves the cursor to the end.
- After failed command → zsh calls `ghostline recover --cmd ... --stderr ...` → `{"type":"recover"}` → daemon first checks the **learned fix cache** (instant replay of a previously accepted fix for this exact failure), then a fast **deterministic** corrector (single-token mistyped command); only on a miss does it call the backend → returns the runnable fix on stdout line 1 and the WHY on line 2. The zsh `precmd` prints the WHY as an explanation and uses `print -z` to **pre-fill the corrected command into the next prompt**, so the user just presses Enter.
- After every command → zsh fires `ghostline context update` (async, fire-and-forget) → `{"type":"update_context"}` → daemon detects git/project info and updates session store.

### Context passed to the LLM

Each request includes session context built up over the shell's lifetime:
- Current working directory
- Git branch and remote repo name (detected via `git -C <cwd> branch --show-current` and `git remote get-url origin`)
- Project type — detected from marker files: `go.mod` → go, `package.json` → node, `requirements.txt`/`pyproject.toml` → python, `Cargo.toml` → rust, `pom.xml`/`build.gradle` → java, `Gemfile` → ruby, `*.tf` → terraform
- Last 5 recent commands (from a rolling window of up to `max_context_commands`)
- **Cross-session "frequently used here"** — up to 5 commands the user has run successfully before in the same repo/directory, pulled from the persistent history log (see below). This is what lets a command typed in one tab be suggested in a brand-new tab.

### Cross-session history (`internal/history/`)

A persistent, append-only **JSONL** log at `~/.ghostline/history.jsonl` records each command for cross-session recall. Stored fields: command, exit code, cwd, git repo, project type, timestamp — **never stderr or command output**.

- `history.Store.Append()` is the single write chokepoint and enforces the secret policy from `redact.go`:
  - `denylisted()` **drops** the whole command if it looks credential-bearing (`KEY=`/`TOKEN=`/`PASSWORD=` assignments, `Authorization:` headers, `aws_secret`, pasted private keys) — it never touches disk.
  - `redact()` masks inline secret values (`--password X`, `-p<pw>` with a letter, `Bearer <tok>`) to `***` for commands that are kept.
- `Frequent(repo, cwd, projectType, limit)` returns distinct **successful** commands relevant to the current context (repo match, else exact cwd match), ranked by frequency with a recency tiebreak.
- The file is compacted to the last 5000 lines on daemon startup (`maxHistoryLines` in `daemon.go`).
- Wired in `daemon.go`: `handleUpdateContext` appends; `handleComplete` queries `Frequent()` and passes the result into the completion prompt.

### Learned error→fix cache (`internal/fixcache/`)

Recovery that learns. When the user **accepts** an LLM recovery — runs the suggested fix and it succeeds — Ghostline records `(failed command, stderr, repo) → (fix, why)` so the next identical failure is fixed **instantly, offline, with no API call**. Persisted as append-only JSONL at `~/.ghostline/recoveries.jsonl`, compacted to the last 2000 entries on startup.

- **Replay** is a new Tier 0 in recovery, ahead of the deterministic and LLM tiers: `handleRecover` calls `fixcache.Lookup(cmd, stderr, repo)` and returns the stored fix verbatim on an exact key match.
- **Acceptance detection** lives in `daemon.go`. `handleRecover` remembers the offered fix per session (only **LLM-tier** results — deterministic typo fixes are already instant, so caching them is pointless). The next `update_context` whose command equals that fix **with exit 0** is the accept signal → `fixcache.Learn(...)`. A non-matching command (e.g. the failed command's own async context-update racing ahead) leaves the offer pending; a 5-minute TTL (`offerTTL`) bounds staleness. A fix that runs but fails is **not** cached.
- **Privacy** — raw stderr is **never** written to disk: only a SHA-256 of the normalized stderr is stored as part of the key (consistent with the project-wide "stderr never touches disk" rule). Commands and fixes are stored **verbatim only when secret-free** — `fixcache` reuses `history.Clean`, and skips caching entirely if the command or fix would be denylisted or redacted (redacting a fix would make it non-runnable). So the file holds no credentials and every replayed fix runs as-is.
- **Most-recent wins** on lookup, so a fix the user re-learned (corrected) supersedes an older one.
- Gated on `history_enabled` (the same local-learning privacy bucket; the strict profile disables it).

### Warp/Fig parity — picker, palette, workflows

Three features that share one wire shape (`completion.Candidate{Command, Description, Source}`) and one zsh chooser (`_ghostline_pick`, which uses `fzf` if installed and falls back to a numbered tty menu). All three load the chosen command into `BUFFER` — never auto-run, consistent with the rest of Ghostline.

- **Described completion menu (Ctrl+X Ctrl+N)** — the "Fig magic". `completer.CompleteMenu()` asks the model for up to 5 full-command candidates, each as `command ||| description` (parsed by `parseMenu`), and the picker shows the descriptions. Distinct from `Ctrl+Space`, which stays the instant single-suffix insert — the menu is the opt-in, browse-with-descriptions path. Wire: `{"type":"complete_menu"}` → `Response.Candidates`.
- **Command palette (Ctrl+X Ctrl+P)** — `{"type":"palette"}` returns saved workflows first, then frequently-used and likely-next commands for the current repo/dir (from `history`), de-duplicated by command (workflows win ties). Works on an empty buffer.
- **Workflows / saved commands (`internal/workflow/`)** — user-authored command templates in `~/.ghostline/workflows.yaml`, managed by `ghostline workflow add|list|remove|show`. `show --set k=v` expands `{{placeholder}}` tokens; unfilled placeholders are left intact for the user to complete in the buffer. Workflows are explicitly authored (not learned), so they are **not** gated by `history_enabled`.

The CLI surface: `ghostline complete-menu` and `ghostline palette` print TSV (`command<TAB>label`) for the shell picker; `ghostline workflow …` manages the store directly (no daemon needed). Keybindings live in `ghostline.zsh` and are rebindable. **Currently zsh-only** — the bash front-end has not been ported (see "Shell integration parity").

### Secret guard at the prompt (`internal/secret/` + `ghostline guard`)

Warns **before** you run a command that would echo, commit, or otherwise expose a credential — closing the hole where a pasted key (e.g. a `gsk_…` Groq key) lands in shell history or a commit before anyone notices.

- **Detector (`internal/secret/`)** — `Match(s) (reason, found)` scans for **value-based** signals: known provider token prefixes (`sk-ant-`, `gsk_`, `sk-`, `ghp_/gho_/…`, `glpat-`, `xox[baprs]-`, `AKIA…`, `AIza…`, `npm_`), secret-named assignments (`*_KEY=`, `*_TOKEN=`, …), `Authorization:`/`Bearer` headers, private-key blocks, and credential-in-URL. It is deliberately **not** entropy-based — a 40-hex git SHA or a UUID is high-entropy but not a secret, and false alarms train users to ignore the guard. The returned reason is a static label and never echoes the matched value.
- **`ghostline guard --buffer <text>`** — a **local-only** subcommand (no daemon, no network, so it's fast enough to gate every Enter and works if the daemon is down). Prints the reason and exits 2 on a hit; prints nothing and exits 0 when clean.
- **Shell integration (`ghostline.zsh`, zsh-only for now)** — overrides the `accept-line` ZLE widget. A cheap local pre-filter (`_ghostline_guard_suspect`) decides whether the line is even worth checking, so ordinary commands pay nothing; only then does it invoke the binary. On a hit it **swallows the first Enter**, prints `⚠ ghostline: this command <reason> — press Enter again to run, or edit the line`, and re-arms: a second Enter on the unchanged buffer runs it; editing the line re-checks. Non-blocking, no `read`. Not yet ported to bash (the readline equivalent is the hard part — see "Shell integration parity").
- **Same detector hardens history** — `history.denylisted()` now calls `secret.Contains`, so commands carrying these tokens are dropped from the JSONL log (and, transitively, from the `fixcache`). History compaction additionally **re-applies the current policy on every startup**, retroactively scrubbing keys stored under older, weaker rules.

### Intent mode (safe single-step automation)

`completer.go` detects when the buffer is a plain-language *goal* rather than a command prefix and asks the model for **one** runnable command that replaces the buffer (the user still reads it and presses Enter — no chaining, no auto-run).

- Triggers (`detectIntent()`): an explicit `#` prefix (`# push to github`), or one of a small set of natural-language lead-ins no real command starts with (`i want`, `i wanna`, `i need`, `i'd like`, `how do i`, `how to`, `please`). This conservative set guarantees normal completion is never hijacked.
- `buildIntentPrompt()` demands exactly one single-line command (no `&&` chains). The response is returned verbatim (via `firstMeaningfulLine`), not treated as a suffix.
- The "next step" emerges naturally: once the user runs the filled command, it lands in recent/history context, so the next `Ctrl+Space` suggests what follows.

### Config file

`~/.ghostline/config.yaml` (created manually; all fields optional):

```yaml
backend: anthropic                      # "anthropic" (default), "openai", or "groq"
setup_complete: true                    # written by `ghostline setup`; daemon waits for this
anthropic_model: claude-haiku-4-5-20251001
anthropic_api_key: ""                   # optional; prefer the ANTHROPIC_API_KEY env var
openai_model: gpt-4o-mini               # openai model (used when backend: openai)
openai_api_key: ""                      # optional; prefer the OPENAI_API_KEY env var
groq_model: llama-3.3-70b-versatile     # groq model (used when backend: groq)
groq_api_key: ""                        # optional; prefer the GROQ_API_KEY env var
completion_timeout_ms: 5000
recovery_timeout_ms: 10000
max_context_commands: 15
history_enabled: true                   # Recommended: redacted local JSONL history
send_cwd: true                          # include current directory/project context in prompts
send_git_remote: true                   # include git remote repo name when available
send_git_status: true                   # include changed file names from git status
send_dir_files: true                    # include nearby file names
send_recent_commands: true              # include recent commands from this shell session
send_stderr: true                       # include stderr in recovery prompts
```

The Claude API key is resolved from `anthropic_api_key` if set, otherwise from `ANTHROPIC_API_KEY` in the environment. OpenAI and Groq resolve the same way (`openai_api_key`/`OPENAI_API_KEY`, `groq_api_key`/`GROQ_API_KEY`). Keep keys in env vars; never commit them.

`ghostline setup --reconfigure` reruns the first-run wizard. The Recommended profile keeps Ghostline useful while remaining explicit: no telemetry/analytics collection, per-user config/history under `~/.ghostline/`, and only terminal context needed for completion/recovery sent to the selected backend. The strict profile disables persisted history, recent-command context, stderr, git remote/status, nearby file names, and cwd context.

### Daemon lifecycle

- Auto-started by `ghostline.zsh` on shell init if setup is complete and the daemon is not running (`ghostline server --background`)
- `EnsureDaemon()` in `daemon.go` also auto-starts it silently before any `complete` or `recover` call, but refuses to start until `ghostline setup` has written `setup_complete: true`
- PID written to `~/.ghostline/ghostline.pid`; stale socket removed on startup
- Sessions are evicted from memory after 24 hours of inactivity (hourly eviction loop in `store.go`)
- Check status: `ghostline status` returns JSON with `status`, `model`, and active session count

### LLM prompt contracts

**Completion** (`internal/completion/prompt.go` + `completer.go`):
- Prompt instructs the model to return ONLY the suffix (text after the buffer). Example: input `git ch` → model returns `eckout`.
- `sanitize()` strips code fences/quotes/whitespace and defensively handles models that echo the whole command back, converting to a pure suffix before concatenating `buffer + suffix`.
- Max tokens: 80. Timeout: `completion_timeout_ms` (default 5s).

**Recovery** (`internal/recovery/correct.go` + `recovery.go`):
- Two tiers (both run on **every** backend — this is the "middle ground"):
  1. **Deterministic** (`tryDeterministic()`): fires on exit 127 / "command not found" for an **unambiguous single-token typo with no arguments** (`gti` → `git`, `dokcer` → `docker`). Instant, offline, no API round-trip — these are high-confidence and can't really be wrong. Uses a hardcoded `knownTypos` map and edit-distance-1 matching against a `commonCommands` allow-list (unique match or nothing). Runs first regardless of backend so trivial typos never cost a network call, even on Claude.
  2. **LLM**: called whenever the deterministic tier returns nil — which includes **any multi-token line** (`gitt status`, `gitt stauts`), so the model corrects the whole line in one pass including mistyped arguments. Model returns `FIX: <runnable command>` + `WHY: <short clause>` or the literal `NONE`.
- `parseResponse()` also defensively splits an inlined `command — explanation` on any Unicode dash (em/en/figure/horizontal-bar/minus-sign) so the FIX stays runnable — critical because the shell pre-fills it into the prompt buffer.
- Max tokens: 120. Timeout: `recovery_timeout_ms` (default 10s).
