package recovery

// Hard-mode edge-case tests for tryDeterministic() and editDistanceWithinOne().
// Run: go test ./internal/recovery/... -run TestCorrectBugs -v

import "testing"

func TestEditDistanceWithinOne(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Single deletions (a has one extra char) — all within 1
		{"grepp", "grep", true},   // delete trailing p
		{"lst", "ls", true},       // delete trailing t
		{"gits", "git", true},     // delete trailing s
		{"maake", "make", true},   // delete one 'a' → make: within 1
		{"maaake", "make", false}, // two extra chars — NOT within 1

		// Single insertions (b has one extra char)
		{"gi", "git", true},       // git is one insertion away
		{"mk", "make", false},     // make is 2 insertions away

		// Single substitutions (same length, one char differs)
		{"got", "git", true},      // o→i
		{"vim", "vim", false},     // identical — function requires diff==1 so returns false
		{"vum", "vim", true},      // u→i

		// Transpositions — NOT handled (edit distance 2 from the perspective of
		// insert/delete/substitute; the function returns false).
		// These are documented limitations: they fall through to the LLM.
		{"gti", "git", false},     // g-[t]-[i] vs g-[i]-[t]: 2 substitutions → NOT within 1
		{"mkae", "make", false},   // m-[k]-[a]-[e] vs m-[a]-[k]-[e]: 2 diffs → NOT within 1
		{"tial", "tail", false},   // t-[i]-[a]-l vs t-[a]-[i]-l: 2 diffs → NOT within 1
		{"psuh", "push", false},   // p-[s]-[u]-h vs p-[u]-[s]-h: 2 diffs → NOT within 1

		// Deletion in middle
		{"kubectl", "kubctl", true},  // kubectl → remove 'e' → kubctl (length diff 1)
		{"docker", "doker", true},    // docker → remove one 'c' or 'd' → within 1

		// Edge: empty string
		{"", "a", true},     // one insertion (a is "" + "a")
		{"a", "", true},     // one deletion
		{"", "", false},     // identical; function requires diff==1
		{"", "ab", false},   // diff 2
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := editDistanceWithinOne(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("editDistanceWithinOne(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestTryDeterministicEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		exit    int
		stderr  string
		wantNil bool
		wantFix string
		note    string
	}{
		// ── Transpositions: fall through to LLM (current limitation) ──────────
		// knownTypos catches gti→git, but an unlisted transposition like "mkae" has
		// edit distance 2 (two char positions differ). tryDeterministic returns nil
		// and the LLM corrects the full line including any argument typos.
		{
			name: "transposition mkae falls through to LLM (by design)",
			cmd:  "mkae", exit: 127, stderr: "command not found: mkae",
			wantNil: true,
			note:    "transpositions not in knownTypos have edit-distance 2 and fall to LLM",
		},
		{
			name: "transposition tial falls through to LLM (by design)",
			cmd:  "tial", exit: 127, stderr: "command not found: tial",
			wantNil: true,
			note:    "tial→tail is a transposition; edit distance 2; deferred to LLM",
		},
		{
			name: "transposition vmi falls through to LLM (by design)",
			cmd:  "vmi", exit: 127, stderr: "command not found: vmi",
			wantNil: true,
			note:    "vmi→vim transposition not in knownTypos; deferred to LLM",
		},

		// ── Known typos map ───────────────────────────────────────────────────
		{name: "lls -> ls", cmd: "lls", exit: 127, stderr: "command not found", wantFix: "ls"},
		{name: "lsl -> ls", cmd: "lsl", exit: 127, stderr: "command not found", wantFix: "ls"},
		{name: "grpe -> grep", cmd: "grpe", exit: 127, stderr: "command not found", wantFix: "grep"},
		{name: "gerp -> grep", cmd: "gerp", exit: 127, stderr: "command not found", wantFix: "grep"},
		{name: "mkdr -> mkdir", cmd: "mkdr", exit: 127, stderr: "command not found", wantFix: "mkdir"},
		{name: "pyhton -> python", cmd: "pyhton", exit: 127, stderr: "command not found", wantFix: "python"},
		{name: "dokcer -> docker", cmd: "dokcer", exit: 127, stderr: "command not found", wantFix: "docker"},
		{name: "claer -> clear", cmd: "claer", exit: 127, stderr: "command not found", wantFix: "clear"},
		{name: "exti -> exit", cmd: "exti", exit: 127, stderr: "command not found", wantFix: "exit"},
		{name: "eixt -> exit", cmd: "eixt", exit: 127, stderr: "command not found", wantFix: "exit"},
		{name: "sudp -> sudo", cmd: "sudp", exit: 127, stderr: "command not found", wantFix: "sudo"},
		{name: "cta -> cat", cmd: "cta", exit: 127, stderr: "command not found", wantFix: "cat"},

		// ── Edit-distance-1 fixes ──────────────────────────────────────────────
		{name: "grepp -> grep (edit-distance-1)", cmd: "grepp", exit: 127, stderr: "command not found: grepp", wantFix: "grep"},

		// ── Must NOT correct ──────────────────────────────────────────────────
		{
			name: "non-127 runtime error not corrected",
			cmd:  "git", exit: 1, stderr: "fatal: not a git repository",
			wantNil: true,
		},
		{
			name:    "completely unknown token returns nil",
			cmd:     "zzqqww", exit: 127, stderr: "command not found: zzqqww",
			wantNil: true,
		},
		{
			name:    "valid command in commonCommands not corrected",
			cmd:     "ls", exit: 127, stderr: "command not found: ls",
			wantNil: true,
		},
		{
			name:    "multi-token defers to LLM",
			cmd:     "gti status", exit: 127, stderr: "command not found: gti",
			wantNil: true,
		},

		// ── Stderr format variations ──────────────────────────────────────────
		{
			name:    "fish shell stderr: Unknown command",
			cmd:     "gti", exit: 127, stderr: "fish: Unknown command: gti",
			wantFix: "git",
		},
		{
			name:    "bash-style stderr",
			cmd:     "gti", exit: 127, stderr: "gti: command not found",
			wantFix: "git",
		},
		{
			name:    "exit 127 no stderr still corrects",
			cmd:     "gti", exit: 127, stderr: "",
			wantFix: "git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tryDeterministic(tt.cmd, tt.exit, tt.stderr)
			if tt.wantNil {
				if got != nil {
					t.Errorf("tryDeterministic(%q) = %+v, want nil\n\tNote: %s", tt.cmd, got, tt.note)
				}
				return
			}
			if got == nil {
				t.Fatalf("tryDeterministic(%q) = nil, want Fix=%q", tt.cmd, tt.wantFix)
			}
			if got.Fix != tt.wantFix {
				t.Errorf("tryDeterministic(%q) Fix = %q, want %q", tt.cmd, got.Fix, tt.wantFix)
			}
			if got.Why == "" {
				t.Errorf("tryDeterministic(%q) Why is empty", tt.cmd)
			}
		})
	}
}
