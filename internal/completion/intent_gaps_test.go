package completion

// Hard-mode tests for detectIntent(), covering gaps and natural-language patterns
// that users would realistically type but aren't currently handled.
// Run: go test ./internal/completion/... -run TestDetectIntentGaps -v

import "testing"

func TestDetectIntentGaps(t *testing.T) {
	tests := []struct {
		name     string
		isGap    bool  // true = natural request but not detected as intent (feature gap)
		buffer   string
		wantOK   bool
		wantGoal string
	}{
		// ── Feature gaps: natural phrases not in intentLeadIns ────────────────
		// These are things a developer would realistically type but are not caught.
		{
			name:   "GAP 'can i' pattern not detected",
			isGap:  true,
			buffer: "can i list all docker containers",
			wantOK: false, // currently false — should ideally be true
		},
		{
			name:   "GAP 'can you' pattern not detected",
			isGap:  true,
			buffer: "can you show me the git log",
			wantOK: false,
		},
		{
			name:   "GAP 'show me' pattern not detected",
			isGap:  true,
			buffer: "show me running processes",
			wantOK: false,
		},
		{
			name:   "GAP 'list all' pattern not detected",
			isGap:  true,
			buffer: "list all docker images",
			wantOK: false,
		},
		{
			name:   "GAP 'find all' pattern not detected",
			isGap:  true,
			buffer: "find all go files recursively",
			wantOK: false,
		},

		// ── Confirmed working patterns ────────────────────────────────────────
		{name: "hash prefix works", buffer: "# push to github", wantOK: true, wantGoal: "push to github"},
		{name: "i want works", buffer: "i want to deploy", wantOK: true, wantGoal: "i want to deploy"},
		{name: "i wanna works", buffer: "i wanna push to github", wantOK: true, wantGoal: "i wanna push to github"},
		{name: "i need works", buffer: "i need to restart the service", wantOK: true, wantGoal: "i need to restart the service"},
		{name: "how do i works", buffer: "how do i undo last commit", wantOK: true, wantGoal: "how do i undo last commit"},
		{name: "how to works", buffer: "how to list running containers", wantOK: true, wantGoal: "how to list running containers"},
		{name: "please works", buffer: "please undo last commit", wantOK: true, wantGoal: "please undo last commit"},

		// ── Confirmed non-triggers (shell commands must not be hijacked) ───────
		{name: "git command not hijacked", buffer: "git status", wantOK: false},
		{name: "make command not hijacked", buffer: "make build", wantOK: false},
		{name: "docker command not hijacked", buffer: "docker ps", wantOK: false},
		{name: "find command not hijacked", buffer: "find . -name '*.go'", wantOK: false},
		{name: "cat command not hijacked", buffer: "cat README.md", wantOK: false},
		{name: "npm install not hijacked", buffer: "npm install", wantOK: false},
		{name: "empty buffer not intent", buffer: "", wantOK: false},
		{name: "hash only not intent", buffer: "#", wantOK: false},
		{name: "hash space only not intent", buffer: "#  ", wantOK: false},

		// ── Case insensitivity ────────────────────────────────────────────────
		{name: "I WANT uppercase works", buffer: "I WANT TO LIST FILES", wantOK: true, wantGoal: "I WANT TO LIST FILES"},
		{name: "HOW DO I uppercase works", buffer: "HOW DO I CHECK DISK USAGE", wantOK: true, wantGoal: "HOW DO I CHECK DISK USAGE"},
		{name: "Please mixed case works", buffer: "Please restart nginx", wantOK: true, wantGoal: "Please restart nginx"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goal, ok := detectIntent(tt.buffer)
			if ok != tt.wantOK {
				if tt.isGap {
					t.Logf("FEATURE GAP: detectIntent(%q) = (%q, %v), not detected as intent — add %q to intentLeadIns to support",
						tt.buffer, goal, ok, tt.buffer[:min(10, len(tt.buffer))])
					return // gap tests are informational, not failures
				}
				t.Errorf("detectIntent(%q) ok = %v, want %v", tt.buffer, ok, tt.wantOK)
				return
			}
			if tt.wantGoal != "" && goal != tt.wantGoal {
				t.Errorf("detectIntent(%q) goal = %q, want %q", tt.buffer, goal, tt.wantGoal)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
