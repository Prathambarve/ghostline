#!/usr/bin/env zsh
# ghostline.zsh — AI-powered terminal assistant
# Source this file in your ~/.zshrc.
#
# Safe to re-source: the function/widget/keybinding definitions below are
# refreshed on every source (so `source ~/.zshrc` picks up code changes), while
# the one-time per-shell setup (session id, stderr capture, hook registration,
# daemon start) runs only once — guarded by _GHOSTLINE_SETUP_DONE, which is NOT
# exported so a fresh shell always re-initializes.

if ! command -v ghostline &>/dev/null; then
    echo "ghostline: binary not found. Run: make install" >&2
    return 1
fi

# ── ZLE: inline completion (Ctrl+Space) ──────────────────────────────────────
# Synchronous on purpose. We call the binary, take its stdout, and write the
# completed command STRAIGHT INTO BUFFER, then park the cursor at the end. The
# binary returns the *full* completed command (it prepends the current buffer
# internally), so a direct assignment is all that's needed.

_ghostline_complete() {
    [[ -z "$BUFFER" ]] && return

    local result
    result="$(ghostline complete --buffer "$BUFFER" --session "$GHOSTLINE_SESSION" 2>/dev/null)"

    # Nothing useful, or no change → leave the buffer untouched.
    [[ -z "$result" ]]           && return
    [[ "$result" == "$BUFFER" ]] && return

    BUFFER="$result"
    CURSOR=${#BUFFER}
    zle redisplay
}
zle -N _ghostline_complete

# Ctrl+Space emits NUL (0x00); zsh normalizes both spellings to "^@".
# NOTE: some terminals (macOS Terminal.app, default iTerm2) do NOT transmit NUL
# on Ctrl+Space. If Ctrl+Space does nothing, either enable "send 0x00" in your
# terminal, or use the Ctrl+X Ctrl+G fallback below (plain control bytes that
# every terminal transmits).
bindkey '^@' _ghostline_complete       # Ctrl+Space
bindkey '^ ' _ghostline_complete       # Ctrl+Space (alternate spelling)
bindkey '^X^G' _ghostline_complete     # Ctrl+X Ctrl+G — terminal-independent fallback

# ── preexec: runs before each command ────────────────────────────────────────

_ghostline_preexec() {
    _GHOSTLINE_LAST_CMD="$1"

    # Record the stderr file size so precmd can read only this command's output.
    if [[ -f "$GHOSTLINE_STDERR" ]]; then
        _GHOSTLINE_STDERR_OFFSET=$(wc -c < "$GHOSTLINE_STDERR" 2>/dev/null | tr -d ' ')
        [[ -z "$_GHOSTLINE_STDERR_OFFSET" ]] && _GHOSTLINE_STDERR_OFFSET=0
    else
        _GHOSTLINE_STDERR_OFFSET=0
    fi
}

# ── precmd: runs after each command ──────────────────────────────────────────

_ghostline_precmd() {
    local exit_code=$?
    local last_cmd="$_GHOSTLINE_LAST_CMD"
    _GHOSTLINE_LAST_CMD=""

    [[ -z "$last_cmd" ]] && return
    [[ "$last_cmd" == ghostline\ * || "$last_cmd" == "ghostline" ]] && return

    ghostline context update \
        --session "$GHOSTLINE_SESSION" \
        --cmd "$last_cmd" \
        --exit-code "$exit_code" \
        --cwd "$PWD" &>/dev/null &!

    if (( exit_code != 0 )); then
        # tee buffers — give it a beat to flush before we read.
        local stderr_content=""
        if [[ -f "$GHOSTLINE_STDERR" ]]; then
            local i
            for i in 1 2 3 4 5; do
                stderr_content="$(tail -c +$((_GHOSTLINE_STDERR_OFFSET + 1)) "$GHOSTLINE_STDERR" 2>/dev/null | head -c 500 | tr -d '\0')"
                [[ -n "$stderr_content" ]] && break
                sleep 0.05
            done
        fi

        if [[ -n "$stderr_content" ]]; then
            local recovery
            recovery="$(ghostline recover \
                --session "$GHOSTLINE_SESSION" \
                --cmd "$last_cmd" \
                --exit-code "$exit_code" \
                --stderr "$stderr_content" 2>/dev/null)"
            if [[ -n "$recovery" ]]; then
                # The binary returns the runnable fix on line 1 and an optional
                # WHY on line 2.
                local fix why
                fix="${recovery%%$'\n'*}"
                why=""
                [[ "$recovery" == *$'\n'* ]] && why="${recovery#*$'\n'}"

                # Show the explanation. Print the prompt-colored prefix with -P,
                # but the body with -r so any '%' in the model's output isn't
                # interpreted as a prompt escape.
                print -nP "\n%F{yellow}↳ ghostline:%f "
                if [[ -n "$why" ]]; then
                    print -r -- "$fix  —  $why"
                else
                    print -r -- "$fix"
                fi

                # Pre-fill the corrected command into the next prompt's buffer so
                # the user can run it by just pressing Enter (or edit/clear it).
                print -z -- "$fix"
            fi
        fi
    fi
}

# ── Hook registration (every source, always exactly one entry) ────────────────
# Filtering out any existing entry before appending keeps the hooks de-duplicated
# across reloads and across an old→new version migration in a running shell.
preexec_functions=(${preexec_functions:#_ghostline_preexec} _ghostline_preexec)
precmd_functions=(${precmd_functions:#_ghostline_precmd} _ghostline_precmd)

# ── One-time per-shell setup ──────────────────────────────────────────────────
# Runs once per shell. NOT exported, so each new shell re-initializes; re-sourcing
# in the same shell skips this (no duplicate hooks, no stacked stderr redirect)
# but still refreshes the function definitions above.

if [[ -z "$_GHOSTLINE_SETUP_DONE" ]]; then
    typeset -g _GHOSTLINE_SETUP_DONE=1

    # Session ID
    export GHOSTLINE_SESSION
    GHOSTLINE_SESSION="$(head -c 8 /dev/urandom | xxd -p 2>/dev/null)"
    [[ -z "$GHOSTLINE_SESSION" ]] && GHOSTLINE_SESSION="$$$(date +%s)"

    # Stderr capture for error recovery.
    # tee keeps the file open across commands. We do NOT truncate it between
    # commands (truncating while tee holds the fd creates a NUL-byte gap up to
    # tee's old write position, which zsh's string variables can't hold). Instead
    # we track the file's byte offset before each command and read only the new
    # bytes after it finishes.
    export GHOSTLINE_STDERR="/tmp/ghostline_stderr_${GHOSTLINE_SESSION}"
    : > "$GHOSTLINE_STDERR"
    exec 2> >(tee -a "$GHOSTLINE_STDERR" >&2)
    trap 'rm -f "$GHOSTLINE_STDERR" 2>/dev/null' EXIT

    typeset -g _GHOSTLINE_LAST_CMD=""
    typeset -g _GHOSTLINE_STDERR_OFFSET=0

    # Auto-start daemon
    {
        ghostline status &>/dev/null || ghostline server --background
    } &>/dev/null &!
fi
