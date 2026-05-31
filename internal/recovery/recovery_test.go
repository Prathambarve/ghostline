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
			name:     "NONE on the FIX line must not leak as a command",
			response: "FIX: NONE",
			wantNil:  true,
		},
		{
			name:     "NONE on FIX line even with a WHY",
			response: "FIX: NONE\nWHY: cannot determine a fix",
			wantNil:  true,
		},
		{
			name:     "lowercase none",
			response: "none",
			wantNil:  true,
		},
		{
			name:     "NONE wrapped in backticks/punctuation",
			response: "`NONE`.",
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

func TestSuggestsInstall(t *testing.T) {
	install := []string{
		"brew install clde && clde --version",
		"sudo apt-get install htop",
		"pip install requests",
		"npm install -g typescript",
		"cargo install ripgrep",
		"PACMAN -S foo", // case-insensitive
	}
	for _, fix := range install {
		if !suggestsInstall(fix) {
			t.Errorf("suggestsInstall(%q) = false, want true", fix)
		}
	}
	notInstall := []string{
		"git status",
		"claude --version",
		"chmod +x ./deploy.sh && ./deploy.sh",
		"cd ../project && make",
	}
	for _, fix := range notInstall {
		if suggestsInstall(fix) {
			t.Errorf("suggestsInstall(%q) = true, want false", fix)
		}
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
			name:     "fuzzy typo with arguments -> defer to LLM for full-line correction",
			cmd:      "gitt status",
			exitCode: 127,
			stderr:   "command not found: gitt",
			wantNil:  true,
		},
		{
			name:     "known typo WITH arguments corrects the command name, keeps args",
			cmd:      "clde --version",
			exitCode: 127,
			stderr:   "zsh: command not found: clde",
			wantFix:  "claude --version",
		},
		{
			name:     "known typo clde -> claude (single token)",
			cmd:      "clde",
			exitCode: 127,
			stderr:   "zsh: command not found: clde",
			wantFix:  "claude",
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
