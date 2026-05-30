package recovery

// Hard-mode edge cases for parseResponse() and indexDash().
// Tests marked "BUG" expose known failures in the current implementation.
// Run: go test ./internal/recovery/... -run TestParseResponseBugs -v

import "testing"

func TestParseResponseBugs(t *testing.T) {
	tests := []struct {
		name    string
		isBug   bool
		desc    string
		input   string
		wantNil bool
		wantFix string
		wantWhy string
	}{
		// ── BUG-3 ─────────────────────────────────────────────────────────────
		// indexDash scans for Unicode dashes to split a "FIX: cmd — reason"
		// inline format. But a real shell argument can contain an em-dash, e.g.
		// a filename like "notes—2024.txt". The parser splits at the em-dash
		// inside the FIX value, corrupting the command.
		//
		// Current:  Fix="cat notes", Why="2024.txt"
		// Desired:  Fix="cat notes—2024.txt", Why="filename with em-dash"
		{
			name:    "BUG em-dash in FIX argument corrupts command",
			isBug:   true,
			desc:    "em-dash inside a filename/arg splits the fix incorrectly",
			input:   "FIX: cat notes—2024.txt\nWHY: filename contains em-dash",
			wantFix: "cat notes—2024.txt",
			wantWhy: "filename contains em-dash",
		},
		{
			name:    "BUG en-dash in argument corrupts command",
			isBug:   true,
			desc:    "en-dash (U+2013) inside an argument splits the fix",
			input:   "FIX: echo \"value–2025\"\nWHY: argument with en-dash",
			wantFix: "echo \"value–2025\"",
			wantWhy: "argument with en-dash",
		},

		// ── Behaviour tests (currently passing) ───────────────────────────────

		// "NONE" with trailing newline — TrimSpace should handle it.
		{
			name:    "NONE with trailing newline returns nil",
			input:   "NONE\n",
			wantNil: true,
		},
		// "NONE" with leading whitespace.
		{
			name:    "NONE with leading whitespace returns nil",
			input:   "  NONE  ",
			wantNil: true,
		},
		// Last FIX line wins when model outputs multiple.
		{
			name:    "last FIX line wins when model emits two",
			input:   "FIX: git statu\nFIX: git status\nWHY: typo in subcommand",
			wantFix: "git status",
			wantWhy: "typo in subcommand",
		},
		// WHY without FIX is not actionable.
		{
			name:    "WHY alone is not actionable",
			input:   "WHY: command not found",
			wantNil: true,
		},
		// FIX is empty after trimming dashes → nil.
		{
			name:    "FIX reduces to empty after dash split → nil",
			input:   "FIX: — reason only",
			wantNil: true,
		},
		// Legitimate inline em-dash format works when no explicit WHY.
		{
			name:    "inline em-dash FIX format when WHY absent",
			input:   "FIX: git status — typo in command name",
			wantFix: "git status",
			wantWhy: "typo in command name",
		},
		// Explicit WHY overrides the inline dash why.
		{
			name:    "explicit WHY overrides inline dash",
			input:   "FIX: git status — inline\nWHY: real reason",
			wantFix: "git status",
			wantWhy: "real reason",
		},
		// ASCII hyphen in command is NOT split (e.g., flags).
		{
			name:    "ASCII hyphen in FIX is NOT split",
			input:   "FIX: git log --oneline\nWHY: typo",
			wantFix: "git log --oneline",
			wantWhy: "typo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseResponse(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("parseResponse(%q) = %+v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseResponse(%q) = nil, want Fix=%q", tt.input, tt.wantFix)
			}
			if got.Fix != tt.wantFix || got.Why != tt.wantWhy {
				if tt.isBug {
					t.Errorf("BUG CONFIRMED — parseResponse(%q) = {Fix:%q Why:%q}, want {Fix:%q Why:%q}\n\tDesc: %s",
						tt.input, got.Fix, got.Why, tt.wantFix, tt.wantWhy, tt.desc)
				} else {
					t.Errorf("REGRESSION — parseResponse(%q) = {Fix:%q Why:%q}, want {Fix:%q Why:%q}",
						tt.input, got.Fix, got.Why, tt.wantFix, tt.wantWhy)
				}
			}
		})
	}
}

func TestIndexDash(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantIndex int // -1 = not found
	}{
		{"em-dash found", "git status — reason", 11},
		{"en-dash found", "cmd – note", 4},
		{"figure dash found", "x ‒ y", 2},
		{"ASCII hyphen NOT found", "git --oneline", -1},
		{"flag not found", "-m", -1},
		{"double dash not found", "--", -1},
		{"no dash at all", "git status", -1},
		{"empty string", "", -1},
		{"dash at start", "— reason", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, _ := indexDash(tt.input)
			if idx != tt.wantIndex {
				t.Errorf("indexDash(%q) index = %d, want %d", tt.input, idx, tt.wantIndex)
			}
		})
	}
}

func TestWHYTruncation(t *testing.T) {
	// WHY is not currently truncated. A very long WHY floods the terminal.
	// This test documents current behavior so a truncation fix can be verified.
	longWHY := "this is a very long why clause that just keeps going and going because the model decided to explain everything in enormous detail which should probably be capped"
	input := "FIX: git status\nWHY: " + longWHY
	got := parseResponse(input)
	if got == nil {
		t.Fatal("expected a result, got nil")
	}
	t.Logf("WHY length = %d chars (no truncation applied — may flood terminal)", len(got.Why))
	// Uncomment and adjust once a cap is implemented:
	// if len(got.Why) > 100 {
	//     t.Errorf("WHY length %d exceeds 100-char cap", len(got.Why))
	// }
}
