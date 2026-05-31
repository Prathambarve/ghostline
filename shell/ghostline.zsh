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

# ── Picker: chooser for the described completion menu ─────────────────────────
# Reads TSV (`command<TAB>label`) on stdin, shows the labels, and echoes the
# chosen command (field 1) on stdout. Uses fzf when available; otherwise a small
# numbered menu. Interactive I/O goes to /dev/tty so it works inside a ZLE widget
# (whose stdin is the piped TSV, not the keyboard).

_ghostline_pick() {
    local input
    input="$(cat)"
    [[ -z "$input" ]] && return 1

    if command -v fzf &>/dev/null; then
        print -r -- "$input" \
            | fzf --delimiter='\t' --with-nth=2 --height=40% --reverse --no-multi </dev/tty \
            | cut -f1
        return
    fi

    # Fallback: numbered menu on the tty.
    local -a cmds labels
    local cmd label
    while IFS=$'\t' read -r cmd label; do
        [[ -z "$cmd" ]] && continue
        cmds+=("$cmd")
        labels+=("$label")
    done <<< "$input"
    (( ${#cmds} == 0 )) && return 1

    local i
    for (( i = 1; i <= ${#labels}; i++ )); do
        print -r -- "  $i) ${labels[i]}" >/dev/tty
    done
    print -n "select [1-${#cmds}]: " >/dev/tty
    local choice
    read -r choice </dev/tty
    [[ "$choice" == <-> ]] && (( choice >= 1 && choice <= ${#cmds} )) && print -r -- "${cmds[choice]}"
}

# Run a TSV-producing ghostline subcommand through the picker and load the chosen
# command into the buffer. Shared by the two widgets below.
_ghostline_pick_into_buffer() {
    local sel
    sel="$("$@" 2>/dev/null | _ghostline_pick)"
    if [[ -n "$sel" ]]; then
        BUFFER="$sel"
        CURSOR=${#BUFFER}
    fi
    zle reset-prompt
}

# Ctrl+X Ctrl+N — described completion menu (Fig-style): pick from several
# candidate completions of the current buffer, each with a short description.
_ghostline_completion_menu() {
    [[ -z "$BUFFER" ]] && return
    _ghostline_pick_into_buffer ghostline complete-menu --buffer "$BUFFER" --session "$GHOSTLINE_SESSION"
}
zle -N _ghostline_completion_menu
bindkey '^X^N' _ghostline_completion_menu

# ── ZLE: secret guard (warn before running a command that leaks a key) ────────
# We override accept-line so that pressing Enter on a command containing secret
# material (an API key, token, password literal, Authorization header…) does NOT
# run it immediately. Instead we print a warning and re-arm: a SECOND Enter on
# the unchanged line runs it, while editing the line re-checks. This closes the
# hole where a pasted key (e.g. gsk_…) lands in shell history or a commit before
# anyone notices.
#
# The expensive authoritative check (the ghostline binary) only runs when a cheap
# local pre-filter sees a plausibly-risky token, so ordinary commands pay nothing.

typeset -g _GHOSTLINE_GUARD_ARMED=""

_ghostline_guard_suspect() {
    # Cheap, broad pre-filter. High recall, low precision on purpose: anything it
    # lets through is confirmed by the binary; anything it rejects is definitely
    # not a secret literal we care about. Lowercased for case-insensitive checks,
    # with a couple of case-sensitive provider prefixes (AKIA/AIza) added.
    local b="${1:l}"
    case "$b" in
        *key=*|*token=*|*secret=*|*password=*|*passwd=*|*credential=*) return 0 ;;
        *authorization:*|*bearer\ *) return 0 ;;
        *sk-*|*gsk_*|*ghp_*|*gho_*|*ghu_*|*ghs_*|*ghr_*|*glpat-*|*xox*|*npm_*) return 0 ;;
        *-----begin*private\ key*) return 0 ;;
    esac
    case "$1" in
        *AKIA*|*AIza*) return 0 ;;
    esac
    return 1
}

_ghostline_accept_line() {
    if [[ -n "$BUFFER" && "$BUFFER" != "$_GHOSTLINE_GUARD_ARMED" ]] && _ghostline_guard_suspect "$BUFFER"; then
        local reason
        reason="$(ghostline guard --buffer "$BUFFER" 2>/dev/null)"
        if [[ -n "$reason" ]]; then
            _GHOSTLINE_GUARD_ARMED="$BUFFER"
            zle -M "⚠ ghostline: this command $reason — press Enter again to run, or edit the line"
            return 0   # swallow this Enter; keep the buffer for review
        fi
    fi
    _GHOSTLINE_GUARD_ARMED=""
    zle .accept-line
}
zle -N accept-line _ghostline_accept_line

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

    ghostline context update \
        --session "$GHOSTLINE_SESSION" \
        --cmd "$last_cmd" \
        --exit-code "$exit_code" \
        --cwd "$PWD" &>/dev/null &!

    # Don't run recovery on ghostline's own commands — but DO record them above
    # so the model learns valid subcommands/args (e.g. "ghostline backend anthropic"
    # ends up in history and future completions of "ghostline backend an" work).
    [[ "$last_cmd" == ghostline || "$last_cmd" == ghostline\ * ]] && return

    # Pipelines report the exit code of the last stage only, so a command like
    # `cat missing.log | grep foo | sort` exits 0 even though cat failed.
    # We also trigger recovery when the overall exit was 0 but stderr contains
    # a recognisable error pattern — this catches silent pipeline failures.
    local _should_recover=0
    (( exit_code != 0 )) && _should_recover=1

    if (( _should_recover == 0 && exit_code == 0 )) && [[ -f "$GHOSTLINE_STDERR" ]]; then
        local _probe
        _probe="$(tail -c +$((_GHOSTLINE_STDERR_OFFSET + 1)) "$GHOSTLINE_STDERR" 2>/dev/null | head -c 200 | tr -d '\0')"
        if [[ "$_probe" == *"No such file or directory"* || \
              "$_probe" == *"Permission denied"* || \
              "$_probe" == *"command not found"* || \
              "$_probe" == *"cannot open"* || \
              "$_probe" == *"not found"* ]]; then
            _should_recover=1
        fi
    fi

    if (( _should_recover )); then
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
                --cwd "$PWD" \
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
