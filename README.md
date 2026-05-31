<div align="center">

# 👻 Ghostline

### The AI in your shell — where the agents can't reach.

**Ghostline lives in your prompt and wins the 2-second moments coding agents were never built for:** the typo, the forgotten flag, the cryptic error, the "why won't this run." It completes what you're typing, fixes what just broke, and *learns from you* — instantly, locally, in the terminal you already use.

```
curl -fsSL https://raw.githubusercontent.com/Prathambarve/ghostline/main/scripts/install.sh | bash
```

Works in **zsh and bash**, on **macOS and Linux**, in **iTerm, Terminal.app, Alacritty, tmux, and over SSH**. No new terminal. No new app. Zero config to start.

</div>

---

## The 30-second pitch

Everyone is building the autonomous coding agent. **Nobody is fixing the other 95% of your terminal time** — the part you drive yourself, keystroke by keystroke, where firing up an agent is overkill and alt-tabbing to a chat window breaks your flow.

That's the gap. You mistype `git ceckout`. A command dies with a wall of stderr. You're on the wrong Python version and don't know it yet. Today you stop, copy the error, switch windows, paste it into Claude, read, switch back. **Ghostline collapses that entire loop into the line you're already on.**

- **It completes** your command with one keystroke.
- **It recovers** from failures automatically — and it understands your *environment*, not just your typo.
- **It learns** every fix you make and replays it for free, forever — a private model of *your* habits that no AI lab can copy, because they never see your shell.

Agents are for what you **delegate**. Ghostline is for everything you **type yourself**. They're complementary — and the second one is wide open.

---

## What it does

### ⚡ Inline completion — `Ctrl+Space`
Press `Ctrl+Space` and Ghostline completes the current command using your context: the directory, the git repo and branch, the project type, your recent commands, and even commands you've run successfully *in this repo before* (across sessions and tabs). It writes the completion straight into your buffer — you review and hit Enter.

```console
$ git ch⎵                    →  git checkout
$ docker run -it --rm ⎵      →  docker run -it --rm -v $(pwd):/app node:20 bash
```

### 🩹 Environment-aware error recovery — automatic
When a command fails, Ghostline reads the error and prints a runnable fix below it, **pre-filled into your next prompt** so you just press Enter. Crucially, it diagnoses the *environment*, not just the text:

```console
$ python app.py
ImportError: ...
↳ ghostline: python3 -m pip install requests && python3 app.py
            — requests is missing; python 3.13 is active but .python-version pins 3.10

$ ./deploy.sh
zsh: permission denied: ./deploy.sh
↳ ghostline: chmod +x ./deploy.sh && ./deploy.sh
            — the script exists but isn't executable

$ npm install
npm WARN EBADENGINE ...
↳ ghostline: nvm use 18.16.0 && npm install
            — node 20.19.5 is active but .nvmrc pins v18.16.0
```

**This is the moat.** It knows which `python` is on your PATH, what version is installed, what your project pins, whether a venv is active — so it catches the "you ran it wrong / your environment is off" failures that are 80% of day-to-day pain. And it knows its lane: a genuine bug in *your source code* (a `SyntaxError`, a failing assertion) gets **silence**, not a wrong guess. Your code is your business; your shell is ours.

### 🧠 Self-correction learning — the flywheel
This is what makes Ghostline get smarter the more you use it. When a command fails and you fix it yourself, Ghostline quietly learns the correction — and the **next** time, it fixes it instantly, **offline, with zero API calls.**

```console
$ znldb start          # fails — Ghostline can't help, stays quiet
$ znldc start          # you fix it yourself ✓   (Ghostline silently learns)
...later, even in a new tab...
$ znldb start
↳ ghostline: znldc start — learned from your earlier correction      (18ms, offline)
```

It's carefully guarded (edit-distance matched, immediate-correction only) so it learns real fixes — not two unrelated commands you happened to type in a row. **This is a private model of your habits that compounds with every keystroke.** No model lab can replicate it; they don't see your terminal.

### 💬 Intent mode — say what you want
Start a line with a natural-language goal and Ghostline turns it into one runnable command (you still press Enter — no auto-run, no chaining).

```console
$ # squash my last 3 commits into one⎵
  →  git reset --soft HEAD~3 && git commit
$ how do i find files bigger than 100mb⎵
  →  find . -type f -size +100M
```

### 🛡️ Secret guard at the prompt *(zsh)*
About to run a command with an API key, token, or password in it? Ghostline catches it **before** it lands in your shell history or a commit — swallows the first Enter, warns you, and runs only if you confirm. Value-based detection (real key prefixes, not just high entropy), so it doesn't cry wolf.

### 📋 Described completion menu — `Ctrl+X Ctrl+N` *(zsh)*
Browse several AI-generated completions, each with a one-line description, and pick one (via `fzf` or a numbered menu). The model writes both the commands *and* the explanations.

---

## Why this wins

| | |
|---|---|
| **Ambient & instant** | It's in the prompt buffer, not a chat window. The unit of value is 2 seconds, not 2 minutes. |
| **The local flywheel** | Per-user, on-device learning of your accepted completions and self-corrections. It compounds. It's the moat. |
| **Privacy-first** | Runs as a local daemon. Secrets are detected and dropped before anything is stored or sent. Raw stderr never touches disk. Your API key stays in your environment — never logged, never committed. |
| **Model-agnostic** | Pluggable backends (Claude, OpenAI, Groq). Never hostage to one lab — switch with one command. |
| **Works everywhere you do** | Any terminal, macOS or Linux, zsh or bash, local or SSH. No new app to adopt. |
| **Degrades gracefully** | Learned fixes and typo corrections run **offline, instantly**, with no network call. |
| **Complementary, not competitive** | Summon an agent when you want one. Ghostline is for everything in between. |

> **The precedent:** Fig proved this exact thesis — autocomplete intelligence layered onto any terminal — and got acquired by Amazon. The market is validated and the incumbent vacated the standalone product. Ghostline picks it up, adds the learning flywheel, and stays cross-shell and privacy-first.

---

## Install

**One line** (downloads a prebuilt, statically-linked binary — no dependencies):

```bash
curl -fsSL https://raw.githubusercontent.com/Prathambarve/ghostline/main/scripts/install.sh | bash
```

Then set a key for your backend of choice and run the consent wizard:

```bash
export GROQ_API_KEY=your-key      # or ANTHROPIC_API_KEY / OPENAI_API_KEY
source ~/.zshrc                   # or ~/.bashrc
ghostline setup                   # pick a backend + privacy profile (Enter = Recommended)
```

<details>
<summary><b>From source</b> (needs Go ≥ 1.21)</summary>

```bash
git clone https://github.com/Prathambarve/ghostline.git
cd ghostline
make build && make install        # uses sudo for /usr/local/bin if needed
export GROQ_API_KEY=your-key
ghostline setup
source ~/.zshrc                   # bash: echo 'source ~/.ghostline/ghostline.bash' >> ~/.bashrc
```
</details>

Verify it's live: `ghostline status` → `{"status":"ok","model":"..."}`. Uninstall anytime with `make uninstall`.

---

## Backends

Inference is pluggable — pick the trade-off you want, switch with `ghostline backend <name>` (persists + restarts the daemon).

| Backend | Default model | Why |
|---|---|---|
| `anthropic` *(default)* | `claude-haiku-4-5` | Fast, cheap, great quality — keeps inline latency low. |
| `groq` | `llama-3.3-70b-versatile` | Extremely low latency, free tier — ideal for the inline path. |
| `openai` | `gpt-4o-mini` | Familiar, broadly available. |

Keys are read from `ANTHROPIC_API_KEY` / `GROQ_API_KEY` / `OPENAI_API_KEY` (or the config file). **They are never hardcoded, committed, or logged.**

---

## Privacy & security

Ghostline sees everything you type, so trust is the product:

- **Local-first.** A background daemon on a Unix socket under `~/.ghostline/`. No telemetry, no analytics.
- **Consent wizard.** First run asks exactly what context Ghostline may use and send. The **strict** profile turns off persisted history, recent-command context, stderr, git/dir context, and the environment probe.
- **Secrets never stored or sent.** A value-based detector (real provider key prefixes, secret assignments, auth headers, private keys) drops credential-bearing commands from history entirely and redacts inline secrets — and the same detector powers the prompt guard.
- **stderr never touches disk.** The learned-fix cache stores only a *hash* of error text, never the raw output.
- **Bounded storage.** The learning cache dedupes by key and stays well under 1 MB.

---

## How it works

```
Your shell  ──(Ctrl+Space / failed command / context)──►  ghostline CLI
                                                              │ Unix socket
                                                              ▼
                                                       ghostline daemon
                                       ┌──────────────────────┼───────────────────────┐
                                       ▼                       ▼                       ▼
                                 completion             error recovery           session + history
                                 (context-aware)   (cache → typo fix → env-aware LLM)   (cross-session, redacted)
                                                              │
                                                     pluggable backend
                                                  (Claude · Groq · OpenAI)
```

Recovery is **tiered for speed and cost**: a previously-learned fix replays instantly (offline); an unambiguous typo is corrected deterministically (offline); only a genuinely new failure calls the model — now armed with a local probe of your environment.

The Go binary is the single, cross-platform source of truth; the shell glue is a thin layer (`ghostline.zsh` full-featured, `ghostline.bash` covers completion + recovery + learning).

---

## Usage

| Action | Keys / command |
|---|---|
| Complete current line | `Ctrl+Space` (fallback `Ctrl+X Ctrl+G`) |
| Browse completion menu *(zsh)* | `Ctrl+X Ctrl+N` |
| Error recovery | automatic, after any failed command |
| Intent → command | type `# <goal>` or `i want…` / `how do i…`, then `Ctrl+Space` |
| Switch backend | `ghostline backend groq` |
| Re-run setup | `ghostline setup --reconfigure` |
| Status | `ghostline status` |

Config lives at `~/.ghostline/config.yaml` (all fields optional — see [`CLAUDE.md`](CLAUDE.md) for the full reference).

---

## Status

Actively developed. Latest release: **v0.2.0** — environment-aware recovery + local self-correction learning. Currently zsh-only: the secret guard and completion menu (recovery, completion, and learning work in both zsh and bash).

Licensing is not yet finalized — reach out to the maintainer before redistributing.

<div align="center">

**Stop leaving your terminal to ask what went wrong. Ghostline already knows.**

</div>
