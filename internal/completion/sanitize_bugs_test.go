package completion

// Hard-mode edge-case tests for sanitize() and firstMeaningfulLine().
// Tests marked "BUG" expose known failures in the current implementation.
// Run: go test ./internal/completion/... -run TestSanitizeBugs -v

import "testing"

func TestSanitizeBugs(t *testing.T) {
	tests := []struct {
		name    string
		isBug   bool // true = test currently FAILS (exposes a real bug)
		desc    string
		buffer  string
		resp    string
		want    string
	}{
		// ── BUG-1 ─────────────────────────────────────────────────────────────
		// Buffer has a trailing space; model echoes the base command WITHOUT the
		// trailing space. HasPrefix check never fires because "git log" does NOT
		// have "git log " as a prefix. Result is a doubled command.
		//
		// Current:  "git log " + "git log" = "git log git log"
		// Desired:  "" (nothing added — model said nothing new)
		{
			name:   "BUG trailing-space buffer echoed without space",
			isBug:  true,
			desc:   "model echoes 'git log' for buffer 'git log ' — should detect as echo and return nothing",
			buffer: "git log ",
			resp:   "git log",
			want:   "",
		},

		// ── BUG-2 ─────────────────────────────────────────────────────────────
		// firstMeaningfulLine does strings.Trim(line, "`\"'") which strips leading
		// AND trailing backticks. Shell command-substitution syntax ``date`` has
		// a backtick at both ends, so both are stripped: "`date`" → "date".
		// The completed command becomes "echo date" instead of "echo `date`".
		{
			name:   "BUG backtick command-substitution suffix stripped",
			isBug:  true,
			desc:   "suffix `date` has its backticks stripped — result should be 'echo `date`'",
			buffer: "echo ",
			resp:   "`date`",
			want:   "echo `date`",
		},

		// ── BUG-3 ─────────────────────────────────────────────────────────────
		// Same as BUG-2 but the model echoes the full command. When the suffix is
		// extracted via the HasPrefix path the backticks survive; when it's returned
		// as a raw suffix they don't. Demonstrates the inconsistency.
		{
			name:   "BUG backtick substitution in suffix form loses backticks",
			isBug:  true,
			desc:   "$(date) in dollar form is safe; backtick form is not",
			buffer: "echo ",
			resp:   "`hostname`",
			want:   "echo `hostname`",
		},

		// ── Already-passing cases ──────────────────────────────────────────────
		// The dollar-paren form is fine because '$' and '(' are not in the Trim set.
		{
			name:   "dollar-paren substitution survives (should pass)",
			isBug:  false,
			buffer: "echo ",
			resp:   "$(date)",
			want:   "echo $(date)",
		},
		// BUG-2b: full-command echo — strings.Trim strips the trailing backtick.
		// "echo `date`" → Trim removes trailing `` ` `` → "echo `date" → wrong.
		{
			name:   "BUG full-command echo with trailing backtick loses trailing backtick",
			isBug:  true,
			desc:   "strings.Trim removes any trailing backtick including ones that close a cmd-substitution",
			buffer: "echo ",
			resp:   "echo `date`",
			want:   "echo `date`",
		},
		// Trailing space + space-prefixed suffix.
		{
			name:   "trailing space buffer with space suffix (should pass)",
			isBug:  false,
			buffer: "git log ",
			resp:   "--oneline",
			want:   "git log --oneline",
		},
		// Buffer with exact same content — nothing added.
		{
			name:   "exact buffer echo returns empty (should pass)",
			isBug:  false,
			buffer: "ls",
			resp:   "ls",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitize(tt.resp, tt.buffer)
			if got != tt.want {
				if tt.isBug {
					t.Errorf("BUG CONFIRMED — sanitize(%q, %q) = %q, want %q\n\tDesc: %s",
						tt.resp, tt.buffer, got, tt.want, tt.desc)
				} else {
					t.Errorf("REGRESSION — sanitize(%q, %q) = %q, want %q",
						tt.resp, tt.buffer, got, tt.want)
				}
			}
		})
	}
}

func TestFirstMeaningfulLineEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"all blank lines", "\n\n\n", ""},
		{"only triple fences", "```\n```", ""},
		// Fixed: backticks are no longer stripped at all — they are shell syntax.
		{"backtick within content preserved (fixed)", "run `tool`", "run `tool`"},
		{"single leading backtick preserved (fixed)", "`word", "`word"},
		{"single trailing backtick preserved (fixed)", "word`", "word`"},
		{"backtick in middle of line survives", "a`b", "a`b"},
		{"empty string", "", ""},
		{"windows line endings", "checkout\r\n", "checkout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstMeaningfulLine(tt.input)
			if got != tt.want {
				t.Errorf("firstMeaningfulLine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
