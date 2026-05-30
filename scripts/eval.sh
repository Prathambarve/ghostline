#!/usr/bin/env bash
#
# Ghostline quality eval — exercises error recovery and autocompletion via the
# CLI (no shell integration needed), so you can eyeball how good the current
# backend is and compare backends side by side.
#
# Usage:
#   make build                        # ensure ./ghostline is current
#   ghostline backend anthropic       # pick a backend (anthropic|openai|groq)
#   ./scripts/eval.sh                 # run the battery
#
# Compare backends:
#   ghostline backend anthropic && ./scripts/eval.sh
#   ghostline backend groq      && ./scripts/eval.sh
#
# Override the binary (defaults to the repo build, which avoids the /usr/local
# bad-block issue):  GL=/usr/local/bin/ghostline ./scripts/eval.sh

set -u
GL="${GL:-./ghostline}"
SID="eval-$$"

hr() { printf '%.0s─' $(seq 1 72); echo; }

# Restart the daemon from $GL so we test current code + current backend/config.
pkill -f "ghostline server" >/dev/null 2>&1
rm -f "$HOME/.ghostline/ghostline.sock"
sleep 0.3

echo "Ghostline eval"
"$GL" backend            # prints the active backend + model
hr

recover() {
  local cmd="$1" ec="$2" err="$3"
  local out
  out="$("$GL" recover --cmd "$cmd" --exit-code "$ec" --stderr "$err" --session "$SID")"
  printf '$ %s\n' "$cmd"
  printf '    exit %s | stderr: %s\n' "$ec" "$err"
  if [ -z "$out" ]; then
    printf '    => (no suggestion)\n\n'
  else
    printf '    => %s\n\n' "$(printf '%s' "$out" | sed '2,$s/^/       /')"
  fi
}

complete() {
  local buf="$1" out
  out="$("$GL" complete --buffer "$buf" --session "$SID")"
  printf '    %-38s => %s\n' "$buf" "${out:-(no completion)}"
}

echo "ERROR RECOVERY"
hr
echo "# single-token typos — should be INSTANT (deterministic, no LLM)"
recover "gti"             127 "zsh: command not found: gti"
recover "dokcer"          127 "zsh: command not found: dokcer"
recover "clera"           127 "zsh: command not found: clera"
echo "# command typo + correct args — LLM full-line"
recover "gitt status"     127 "zsh: command not found: gitt"
recover "dokcer ps -a"    127 "zsh: command not found: dokcer"
echo "# double typo — LLM should fix BOTH"
recover "gitt stauts"     127 "zsh: command not found: gitt"
recover 'gti cmomit -m "wip"' 127 "zsh: command not found: gti"
echo "# real errors (not typos) — does it give a useful fix?"
recover "git push"        128 "fatal: The current branch main has no upstream branch.
To push the current branch and set the remote as upstream, use git push --set-upstream origin main"
recover "python app.py"   1   "ModuleNotFoundError: No module named 'requests'"
recover "npm run dev"     1   "npm ERR! Missing script: \"dev\""
recover "tar -xzf notes.txt" 2 "tar: This does not look like a tar archive"
recover "cat fil.txt"     1   "cat: fil.txt: No such file or directory"
recover "kubectl get pods --all" 1 "error: unknown flag: --all"
echo "# no real fix — should NOT hallucinate (expect no suggestion)"
recover "ls"              0   ""

hr
echo "AUTOCOMPLETION  (cwd: $(pwd) — a Go repo, so it has project context)"
hr
echo "# common prefixes"
complete "git ch"
complete "git com"
complete "git log --on"
complete "git push --set-"
complete "docker ru"
complete "docker run -"
complete "kubectl get po"
complete "go te"
complete "npm ru"
echo "# flags"
complete "tar -c"
complete "grep -r"
echo "# intent mode — natural language => ONE command"
complete "# list all go files recursively"
complete "# undo the last git commit but keep the changes"
complete "i wanna push to github"
complete "# show disk usage of the current folder, human readable"
hr
echo "Note: if everything shows '(no completion)' / '(no suggestion)', the backend"
echo "isn't ready — check 'ghostline backend' for a key warning."
