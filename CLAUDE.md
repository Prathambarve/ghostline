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
internal/completion/menu.go  — Multi-candidate described completions (Candidate type) behind the Fig-style picker
internal/completion/         — Completion prompt builder + suffix sanitizer + intent-mode prompt
internal/recovery/           — Error recovery: deterministic typo corrector + LLM prompt/parser
internal/envprobe/           — Environment fact gatherer (tool version/path, version-file pins, venv) feeding env-aware recovery; runs client-side in `ghostline recover`
internal/nextstep/           — Proactive next-step prediction: LLM workflow reasoning + a learned cache (predict once → replay offline) behind the grey ghost-text suggestion
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
| Next-step prediction (grey ghost text) | ✅ | ❌ readline has no `POSTDISPLAY` |

**Why it's not a copy-paste port:** the zsh integration is ZLE-based (`zle -N`, `BUFFER`/`CURSOR`, `zle -M`, the `accept-line` override, `${1:l}`, `print -z`, 1-indexed arrays, the `<->` glob). Bash uses readline instead (`bind -x` with `READLINE_LINE`/`READLINE_POINT`), has no ZLE, no native preexec (DEBUG trap), and 0-indexed arrays. Each feature needs a bash reimplementation. Specifically:
- **Completion menu** ports cleanly via `bind -x` rewriting `READLINE_LINE`; the `_ghostline_pick` helper is mostly portable (drop the `<->` glob and 1-indexed arrays).
- **Secret guard** is the hard one: bash readline has no clean equivalent of overriding `accept-line` to "swallow Enter, warn, run on the second Enter." A bash port likely has to bind Return via `bind -x` and re-issue the command, which is fiddly and can conflict with other readline setups.

When adding a new shell-facing feature, implement it in `ghostline.zsh` AND `ghostline.bash` (or explicitly record the gap here). New keybindings should use plain control-byte chords like `^X^N` (every terminal transmits these) rather than `Ctrl+Space`/NUL, which several terminals (macOS Terminal.app, default iTerm2) do not send.

### Request flow

The shell integration (`ghostline.zsh`) communicates with the Go binary exclusively through CLI subcommands. The binary communicates with the daemon over a Unix domain socket at `~/.ghostline/ghostline.sock`.

- `Ctrl+Space` → zsh calls `ghostline complete --buffer <text> --session <id>` → binary sends `{"type":"complete"}` to daemon → daemon calls the backend → returns the full completed command to stdout. The zsh widget writes that string straight into `BUFFER` and moves the cursor to the end.
- After failed command → zsh calls `ghostline recover --cmd ... --stderr ... --cwd ...`. Before sending, the `recover` CLI (a child of the interactive shell, so it sees the real PATH/`$VIRTUAL_ENV` the detached daemon cannot) runs an **environment probe** (`internal/envprobe`) for the failing tool and attaches the facts as `env_context`. → `{"type":"recover"}` → daemon first checks the **learned fix cache** (instant replay of a previously accepted fix for this exact failure), then a fast **deterministic** corrector (mistyped command name); only on a miss does it call the backend with the env facts → returns the runnable fix on stdout line 1 and the WHY on line 2. The zsh `precmd` prints the WHY as an explanation and uses `print -z` to **pre-fill the corrected command into the next prompt**, so the user just presses Enter.
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

Recovery that learns. When the user **accepts** an LLM recovery — runs the suggested fix and it succeeds — Ghostline records `(failed command, stderr, repo) → (fix, why)` so the next identical failure is fixed **instantly, offline, with no API call**. Persisted as append-only JSONL at `~/.ghostline/recoveries.jsonl`. Compaction **dedupes by key** — collapsing each `(cmd, stderr-hash, repo)` to its most-recent entry (lossless, since `Lookup` already takes most-recent) — then trims to the last 2000. It runs on startup **and** as a runtime safety net once appends since the last compaction hit the cap, so a never-restarted daemon stays bounded. `Learn` also skips a redundant append when the newest entry for a key already replays the same fix, so re-running a known fix (replay-then-rerun) never grows the file. Net effect: file size tracks *distinct learned failures* (≤2000 × ~300 B ≈ **under 1 MB**), not total recovery events.

- **Replay** is a new Tier 0 in recovery, ahead of the deterministic and LLM tiers: `handleRecover` calls `fixcache.Lookup(cmd, stderr, repo)` and returns the stored fix verbatim on an exact key match.
- **Self-correction learning** (the offline flywheel): Ghostline also learns fixes it never offered. `handleRecover` records the last failed command per session (with its real stderr, so the learned key matches a future lookup); when the **immediately-following** command succeeds and `looksLikeCorrection(failed, fixed)` holds, `handleUpdateContext` learns `failed → fixed` with why `"learned from your earlier correction"`. So if the model abstains (`NONE`) on `clde` and you type `claude` yourself, the *second* `clde` replays `claude` instantly and offline. `looksLikeCorrection` requires both the whole command and the first token (command name) to be a close edit — an OSA edit distance (Levenshtein + adjacent transpositions) within ~one edit per three characters, with a length floor — so unrelated back-to-back commands (`ls`→`cd`, `git status`→`git push`, `cat a.txt`→`rm a.txt`) are never learned. Only the immediately-next command is considered (any other success consumes the pending failure); a 2-minute TTL bounds staleness.
- **Acceptance detection** lives in `daemon.go`. `handleRecover` remembers the offered fix per session (only **LLM-tier** results — deterministic typo fixes are already instant, so caching them is pointless). The next `update_context` whose command equals that fix **with exit 0** is the accept signal → `fixcache.Learn(...)`. A non-matching command (e.g. the failed command's own async context-update racing ahead) leaves the offer pending; a 5-minute TTL (`offerTTL`) bounds staleness. A fix that runs but fails is **not** cached.
- **Privacy** — raw stderr is **never** written to disk: only a SHA-256 of the normalized stderr is stored as part of the key (consistent with the project-wide "stderr never touches disk" rule). Commands and fixes are stored **verbatim only when secret-free** — `fixcache` reuses `history.Clean`, and skips caching entirely if the command or fix would be denylisted or redacted (redacting a fix would make it non-runnable). So the file holds no credentials and every replayed fix runs as-is.
- **Most-recent wins** on lookup, so a fix the user re-learned (corrected) supersedes an older one.
- Gated on `history_enabled` (the same local-learning privacy bucket; the strict profile disables it).

### Described completion menu (Ctrl+X Ctrl+N)

The "Fig magic": browse several AI-generated completions, each with a one-line description, and pick one.

- `completer.CompleteMenu()` asks the model for up to 5 full-command candidates, each as `command ||| description` (parsed by `parseMenu`), returned on the wire as `completion.Candidate{Command, Description}`. Wire: `{"type":"complete_menu"}` → `Response.Candidates`.
- The zsh widget (`_ghostline_completion_menu`) renders them through `_ghostline_pick` — a chooser that uses `fzf` if installed and falls back to a numbered tty menu — and loads the chosen command into `BUFFER` (never auto-run). `ghostline complete-menu` prints the TSV the picker consumes.
- Distinct from `Ctrl+Space`, which stays the instant single-suffix insert — the menu is the opt-in, browse-with-descriptions path. **Currently zsh-only** (see "Shell integration parity").

This is kept because it's genuinely AI (the model writes the candidates *and* their descriptions). Static-recall conveniences — saved-command/alias managers and fuzzy command palettes — were deliberately **not** kept: a non-AI tool (`alias`, `navi`, `fzf`, `Ctrl+R`, `atuin`) already does them, so they're off-mission for "AI in your shell." Don't re-add them.

### Secret guard at the prompt (`internal/secret/` + `ghostline guard`)

Warns **before** you run a command that would echo, commit, or otherwise expose a credential — closing the hole where a pasted key (e.g. a `gsk_…` Groq key) lands in shell history or a commit before anyone notices.

- **Detector (`internal/secret/`)** — `Match(s) (reason, found)` scans for **value-based** signals: known provider token prefixes (`sk-ant-`, `gsk_`, `sk-`, `ghp_/gho_/…`, `glpat-`, `xox[baprs]-`, `AKIA…`, `AIza…`, `npm_`), secret-named assignments (`*_KEY=`, `*_TOKEN=`, …), `Authorization:`/`Bearer` headers, private-key blocks, and credential-in-URL. It is deliberately **not** entropy-based — a 40-hex git SHA or a UUID is high-entropy but not a secret, and false alarms train users to ignore the guard. The returned reason is a static label and never echoes the matched value.
- **`ghostline guard --buffer <text>`** — a **local-only** subcommand (no daemon, no network, so it's fast enough to gate every Enter and works if the daemon is down). Prints the reason and exits 2 on a hit; prints nothing and exits 0 when clean.
- **Shell integration (`ghostline.zsh`, zsh-only for now)** — overrides the `accept-line` ZLE widget. A cheap local pre-filter (`_ghostline_guard_suspect`) decides whether the line is even worth checking, so ordinary commands pay nothing; only then does it invoke the binary. On a hit it **swallows the first Enter**, prints `⚠ ghostline: this command <reason> — press Enter again to run, or edit the line`, and re-arms: a second Enter on the unchanged buffer runs it; editing the line re-checks. Non-blocking, no `read`. Not yet ported to bash (the readline equivalent is the hard part — see "Shell integration parity").
- **Same detector hardens history** — `history.denylisted()` now calls `secret.Contains`, so commands carrying these tokens are dropped from the JSONL log (and, transitively, from the `fixcache`). History compaction additionally **re-applies the current policy on every startup**, retroactively scrubbing keys stored under older, weaker rules.

### Environment-aware recovery (`internal/envprobe/`)

The recovery moat: diagnose **shell/environment** failures precisely instead of guessing — "you're on python 3.13 but `.python-version` pins 3.10, use that", "`htop` isn't installed, `brew install htop`" — so the user never has to leave the terminal to ask an agent what went wrong. The hard boundary: **environment/invocation errors are in scope; bugs inside the user's own source code are not.** A `SyntaxError` or failing assertion in their file → the model returns `NONE` and Ghostline stays silent.

- **`envprobe.Probe(cmd, cwd) Facts`** gathers, for the command's first token (the tool): `which`-resolved path (via `exec.LookPath`, no subprocess), installed version (`<tool> --version`, for a curated safe-flag allow-list, bounded to ~300ms), version-manager pins read from the project (`.python-version`/`.nvmrc`/`.node-version`/`.ruby-version`/`.java-version` raw, `go.mod` `go` directive, `.tool-versions` asdf, `rust-toolchain.toml` channel), and the active `$VIRTUAL_ENV`. When the tool is **not** on PATH it reports only that it is missing — it deliberately does **not** name package managers or suggest an install (see below). `Facts.Prompt()` renders a compact block; empty when nothing useful is found.
- **Path-form commands** (token contains `/`, e.g. `./deploy.sh`) are treated as local files, not PATH binaries: the probe stats the file and reports its mode (`exists but is not executable` / `is a directory…` / `does not exist`) instead of a misleading "not on PATH" + package-manager suggestion — this is the common exit-126 "permission denied" case, which then yields `chmod +x …`.
- **The version exec is gated on a declared pin being present** — the installed version only adds diagnostic signal when there's a project pin to contrast it against (the mismatch story). With no pin, the (only) subprocess is skipped, so self-explanatory errors pay nothing for it.
- **Runs client-side, in `ghostline recover`** — that process is a child of the interactive shell and inherits its real PATH/`$VIRTUAL_ENV`; the detached daemon would see neither. Both `exec.LookPath` and the version exec are behind test seams (`lookPath`, `execRunner`) so unit tests don't depend on the host toolchain.
- **No install suggestions for "command not found"** — a guessed `brew install <X>` for what may be a typo can't be verified offline and erodes trust (it once turned `clde` into `brew install clde`). So: the probe omits package managers; the recovery prompt tells the model to correct the typo or return `NONE`, never to install the missing program; and `recovery.go` has a deterministic backstop (`suggestsInstall`) that drops any install-form fix for a not-found failure. Typo corrections come from `tryDeterministic` (now also correcting a curated typo's command name **with arguments** — `clde --version` → `claude --version`) or the model returning the intended command.
- **Privacy** — gated by `send_env_probe` (Recommended on, strict off). Probe output (paths/versions) is only ever placed in the in-flight prompt; it is **never written to disk**. The daemon also blanks `env_context` when the knob is off (defense in depth).
- The probe cost is paid before the socket call, so even fixcache/deterministic hits pay it; kept negligible by doing only fast PATH/file reads plus one bounded version exec, degrading gracefully to whatever was gathered on timeout. Recovery is not the latency-critical path (that's `Ctrl+Space`).

### Next-step prediction (`internal/nextstep/` + grey ghost text)

Proactively predicts the **next step in your workflow** — including a command you've never run here (`terraform plan` → `apply`, `git clone X` → `cd X && …`, a failure → its rollback). This is the part only a model can do: frecency/history (`history.Successors`, zsh-autosuggestions, zoxide) can only replay paths already walked; this *reasons forward*. Shown as grey ghost text on an empty prompt, accepted with `Ctrl+Space`.

- **Engine (`internal/nextstep/`)** — `ShouldPredict(cmd, exitCode)` gates to workflow-bearing moments (a curated first-token allowlist: terraform/git/docker/npm/make/kubectl/…) **or any failure**; trivial commands (`ls`/`cd`/`cat`) → nothing. `buildPrompt` feeds the recent-command **trajectory** + cwd/git/project and asks for ONE next command; `parseResult` reads `NEXT:`/`RISK:` (or `NONE`). `isDestructive` flags irreversible steps (apply/destroy/force-push/rm/delete/deploy…) — applied even if the model says "safe."
- **The moat mechanic** — the LLM makes the semantic leap once; a learned cache (`cache.go`, same dedupe/compaction/secret-gating as `fixcache`, keyed `(cmd, repo)`) replays it **instantly and offline** next time. Frecency can't propose the unseen step; the model can; the cache makes it free on repeat.
- **Daemon** — `handlePredict`: gate → cache `Lookup` (instant) → on miss call the backend → cache `Learn`. Returns `{prediction, destructive}`. Gated on `predict_enabled`.
- **CLI** — `ghostline predict --session --cwd --last-cmd --exit-code` prints the predicted command (line 1) and `destructive` (line 2) when flagged.
- **Shell (`ghostline.zsh`, zsh-only)** — `precmd` stashes `(cmd, exit)` unless recovery just pre-filled the buffer (recovery owns failures it can fix; prediction handles the rest). A `line-init` hook (registered via the conflict-safe `add-zle-hook-widget`) fires an async `ghostline predict` over a process-substitution pipe watched by `zle -F`; when it lands, a grey `POSTDISPLAY` renders it (red + ⚠ for destructive). `Ctrl+Space` on an empty buffer accepts it into `BUFFER` (you still press Enter — never auto-run); a `line-pre-redraw` hook clears the ghost as soon as you type. **Not ported to bash** — readline has no `POSTDISPLAY` (see parity table).
- **Stays inside the two hard rules**: off the typing hot path (async, post-command — `Ctrl+Space` completion is untouched), and one step at a time with a human keystroke to run it (intent-mode's proactive cousin; no chaining, no auto-run).

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
send_env_probe: true                    # probe tool version/path + project version pins on errors (env-aware recovery)
predict_enabled: true                   # proactive next-step prediction (grey ghost text after workflow commands)
```

The Claude API key is resolved from `anthropic_api_key` if set, otherwise from `ANTHROPIC_API_KEY` in the environment. OpenAI and Groq resolve the same way (`openai_api_key`/`OPENAI_API_KEY`, `groq_api_key`/`GROQ_API_KEY`). Keep keys in env vars; never commit them.

`ghostline setup --reconfigure` reruns the first-run wizard. The Recommended profile keeps Ghostline useful while remaining explicit: no telemetry/analytics collection, per-user config/history under `~/.ghostline/`, and only terminal context needed for completion/recovery sent to the selected backend. The strict profile disables persisted history, recent-command context, stderr, git remote/status, nearby file names, cwd context, the environment probe, and next-step prediction.

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
  1. **Deterministic** (`tryDeterministic()`): fires on exit 127 / "command not found". A **curated `knownTypos` entry corrects the command name even with arguments** (`gti status` → `git status`, `clde --version` → `claude --version`) — exact-map matches are high-confidence, so the args are kept verbatim. **Fuzzy** edit-distance-1 matching against the `commonCommands` allow-list stays **single-token only** (with args, a near-miss token might be intended), unique match or nothing. Instant, offline, no API round-trip; runs first regardless of backend.
  2. **LLM**: called whenever the deterministic tier returns nil — which includes **any multi-token line** (`gitt status`, `gitt stauts`), so the model corrects the whole line in one pass including mistyped arguments. The prompt carries the **environment facts** from `envprobe` (when `send_env_probe`) and a sharpened **scope rule**: fix only how a command was invoked or the environment it ran in (misspelled command, wrong tool version/interpreter, missing module, inactive venv, missing env var, permissions, wrong dir, bad flags); for a genuine bug in the user's **own source code** (syntax error, exception from their logic, failing assertion) return `NONE` and stay silent. For a "command not found", the prompt says to correct the typo or return `NONE` — **never** suggest installing the missing program; the `suggestsInstall` backstop in `recovery.go` drops any install-form fix for a not-found failure regardless (so `clde` can't become `brew install clde`). Model returns `FIX: <runnable command>` + `WHY: <one or two clauses, citing env facts when relevant>` or the literal `NONE`.
- `parseResponse()` also defensively splits an inlined `command — explanation` on any Unicode dash (em/en/figure/horizontal-bar/minus-sign) so the FIX stays runnable — critical because the shell pre-fills it into the prompt buffer.
- Max tokens: 120. Timeout: `recovery_timeout_ms` (default 10s).
