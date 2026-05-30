#!/usr/bin/env bash
# ghostline.bash — AI-powered terminal assistant for bash
# Source this in ~/.bashrc:  source ~/.ghostline/ghostline.bash
#
# Safe to re-source: function/binding definitions are refreshed on every source;
# one-time per-shell setup (session id, stderr capture, daemon start) runs only
# once, guarded by _GHOSTLINE_SETUP_DONE.

if ! command -v ghostline &>/dev/null; then
    echo "ghostline: binary not found. Run: curl -fsSL https://ghostline.dev/install.sh | sh" >&2
    return 1
fi

# ── Readline: inline completion (Ctrl+Space) ──────────────────────────────────
# bind -x runs a shell function with READLINE_LINE/READLINE_POINT set to the
# current buffer. We overwrite them to inject the completed command.

_ghostline_complete() {
    local buf="${READLINE_LINE}"
    [[ -z "$buf" ]] && return
    # Don't AI-complete ghostline's own subcommands — model has no knowledge of
    # our CLI args and will hallucinate (e.g. "anth" → "anthology").
    [[ "$buf" == ghostline || "$buf" == ghostline\ * ]] && return

    local result
    result="$(ghostline complete --buffer "$buf" --session "$GHOSTLINE_SESSION" 2>/dev/null)"
    [[ -z "$result" || "$result" == "$buf" ]] && return

    READLINE_LINE="$result"
    READLINE_POINT=${#result}
}

bind -x '"\C- ": _ghostline_complete'    # Ctrl+Space
bind -x '"\C-x\C-g": _ghostline_complete' # Ctrl+X Ctrl+G — universal fallback

# ── preexec via DEBUG trap ────────────────────────────────────────────────────
# Bash has no native preexec hook. We use a DEBUG trap + a flag set at the END
# of PROMPT_COMMAND so the trap only captures the very next user command, not
# commands run inside PROMPT_COMMAND itself.

_GHOSTLINE_NEXT_IS_USER_CMD=0

_ghostline_debug_trap() {
    if (( _GHOSTLINE_NEXT_IS_USER_CMD )); then
        _GHOSTLINE_NEXT_IS_USER_CMD=0
        _GHOSTLINE_LAST_CMD="$BASH_COMMAND"
        if [[ -f "$GHOSTLINE_STDERR" ]]; then
            _GHOSTLINE_STDERR_OFFSET=$(wc -c < "$GHOSTLINE_STDERR" 2>/dev/null | tr -d ' ')
            [[ -z "$_GHOSTLINE_STDERR_OFFSET" ]] && _GHOSTLINE_STDERR_OFFSET=0
        else
            _GHOSTLINE_STDERR_OFFSET=0
        fi
    fi
    return 0  # must return 0 — non-zero prevents the command from running
}
trap '_ghostline_debug_trap' DEBUG

# ── postcmd via PROMPT_COMMAND ────────────────────────────────────────────────

_ghostline_precmd() {
    local exit_code=$?
    local last_cmd="${_GHOSTLINE_LAST_CMD:-}"
    _GHOSTLINE_LAST_CMD=""

    [[ -z "$last_cmd" ]] && return
    [[ "$last_cmd" == ghostline || "$last_cmd" == ghostline\ * ]] && return

    # Context update: async, fire-and-forget
    ( ghostline context update \
        --session "$GHOSTLINE_SESSION" \
        --cmd "$last_cmd" \
        --exit-code "$exit_code" \
        --cwd "$PWD" &>/dev/null ) &

    # Pipeline exit code fix: also trigger recovery when stderr has error
    # patterns even if the overall exit was 0 (e.g. cat bad.log | sort).
    local _should_recover=0
    (( exit_code != 0 )) && _should_recover=1

    if (( _should_recover == 0 )) && [[ -f "$GHOSTLINE_STDERR" ]]; then
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
                local fix why
                fix="${recovery%%$'\n'*}"
                why=""
                [[ "$recovery" == *$'\n'* ]] && why="${recovery#*$'\n'}"

                printf '\n\033[33m↳ ghostline:\033[0m '
                if [[ -n "$why" ]]; then
                    printf '%s  —  %s\n' "$fix" "$why"
                else
                    printf '%s\n' "$fix"
                fi

                # In bash there's no print -z to pre-fill the prompt buffer.
                # Add the fix to history so the user can press Up to accept it.
                history -s "$fix"
            fi
        fi
    fi
}

# Wire into PROMPT_COMMAND (de-duplicated across re-sources).
# The assignment _GHOSTLINE_NEXT_IS_USER_CMD=1 runs AFTER _ghostline_precmd so
# commands inside precmd don't get captured by the DEBUG trap.
_GHOSTLINE_PRECMD_ENTRY='_ghostline_precmd; _GHOSTLINE_NEXT_IS_USER_CMD=1'
if [[ -z "${PROMPT_COMMAND:-}" ]]; then
    PROMPT_COMMAND="$_GHOSTLINE_PRECMD_ENTRY"
elif [[ "$PROMPT_COMMAND" != *"_ghostline_precmd"* ]]; then
    PROMPT_COMMAND="${_GHOSTLINE_PRECMD_ENTRY}${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
fi

# ── One-time per-shell setup ──────────────────────────────────────────────────

if [[ -z "${_GHOSTLINE_SETUP_DONE:-}" ]]; then
    _GHOSTLINE_SETUP_DONE=1

    export GHOSTLINE_SESSION
    GHOSTLINE_SESSION="$(head -c 8 /dev/urandom | xxd -p 2>/dev/null)"
    [[ -z "$GHOSTLINE_SESSION" ]] && GHOSTLINE_SESSION="${$}$(date +%s)"

    export GHOSTLINE_STDERR="/tmp/ghostline_stderr_${GHOSTLINE_SESSION}"
    : > "$GHOSTLINE_STDERR"
    exec 2> >(tee -a "$GHOSTLINE_STDERR" >&2)
    trap 'rm -f "$GHOSTLINE_STDERR" 2>/dev/null' EXIT

    _GHOSTLINE_LAST_CMD=""
    _GHOSTLINE_STDERR_OFFSET=0
    _GHOSTLINE_NEXT_IS_USER_CMD=1  # first prompt is ready — next cmd is user's

    # Auto-start daemon
    { ghostline status &>/dev/null || ghostline server --background; } &>/dev/null &
fi
