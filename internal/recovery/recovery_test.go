package recovery

import "testing"

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		wantNil  bool
		wantFix  string
		wantWhy  string
	}{
		{
			name:     "fix and why",
			response: "FIX: git status\nWHY: you typed gti",
			wantFix:  "git status",
			wantWhy:  "you typed gti",
		},
		{
			name:     "fix only (why omitted for trivial)",
			response: "FIX: ls -la",
			wantFix:  "ls -la",
			wantWhy:  "",
		},
		{
			name:     "surrounding whitespace and blank lines",
			response: "\n  FIX: sudo apt install foo  \n\n  WHY: needs root  \n",
			wantFix:  "sudo apt install foo",
			wantWhy:  "needs root",
		},
		{
			name:     "model inlines explanation into FIX with em-dash",
			response: "FIX: git status — typo in command and subcommand",
			wantFix:  "git status",
			wantWhy:  "typo in command and subcommand",
		},
		{
			name:     "inlined em-dash does not override an explicit WHY",
			response: "FIX: git status — inline reason\nWHY: real reason",
			wantFix:  "git status",
			wantWhy:  "real reason",
		},
		{
			name:     "literal NONE",
			response: "NONE",
			wantNil:  true,
		},
		{
			name:     "empty",
			response: "",
			wantNil:  true,
		},
		{
			name:     "why without fix is not actionable",
			response: "WHY: something went wrong",
			wantNil:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseResponse(tt.response)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("parseResponse(%q) = %+v, want nil", tt.response, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseResponse(%q) = nil, want Fix=%q", tt.response, tt.wantFix)
			}
			if got.Fix != tt.wantFix || got.Why != tt.wantWhy {
				t.Errorf("parseResponse(%q) = {Fix:%q Why:%q}, want {Fix:%q Why:%q}",
					tt.response, got.Fix, got.Why, tt.wantFix, tt.wantWhy)
			}
		})
	}
}

func TestTryDeterministic(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		exitCode int
		stderr   string
		wantNil  bool
		wantFix  string
	}{
		{
			name:     "known typo gti -> git (single token)",
			cmd:      "gti",
			exitCode: 127,
			stderr:   "zsh: command not found: gti",
			wantFix:  "git",
		},
		{
			name:     "edit-distance-1 grepp -> grep (single token)",
			cmd:      "grepp",
			exitCode: 127,
			stderr:   "command not found: grepp",
			wantFix:  "grep",
		},
		{
			name:     "short typo sl -> ls",
			cmd:      "sl",
			exitCode: 127,
			stderr:   "command not found",
			wantFix:  "ls",
		},
		{
			name:     "has arguments -> defer to LLM for full-line correction",
			cmd:      "gitt stauts",
			exitCode: 127,
			stderr:   "command not found: gitt",
			wantNil:  true,
		},
		{
			name:     "real command real error is left to the LLM",
			cmd:      "git push",
			exitCode: 1,
			stderr:   "fatal: no upstream branch",
			wantNil:  true,
		},
		{
			name:     "valid command not corrected",
			cmd:      "git",
			exitCode: 127, // contrived; corrector must not touch a real command
			stderr:   "command not found",
			wantNil:  true,
		},
		{
			name:     "unrecognizable typo falls through",
			cmd:      "qwzzx",
			exitCode: 127,
			stderr:   "command not found: qwzzx",
			wantNil:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tryDeterministic(tt.cmd, tt.exitCode, tt.stderr)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("tryDeterministic(%q) = %+v, want nil", tt.cmd, got)
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
				t.Errorf("tryDeterministic(%q) Why is empty, want an explanation", tt.cmd)
			}
		})
	}
}
