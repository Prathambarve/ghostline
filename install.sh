#!/usr/bin/env bash
# The Ghostline installer now lives at scripts/install.sh — it downloads a
# prebuilt, statically-linked binary (no Go, Homebrew, or local model required;
# inference uses a cloud backend: Claude, Groq, or OpenAI).
#
# Preferred one-liner:
#   curl -fsSL https://raw.githubusercontent.com/Prathambarve/ghostline/main/scripts/install.sh | bash
#
# This shim keeps `bash install.sh` working from a cloned repo.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
exec bash "$HERE/scripts/install.sh" "$@"
