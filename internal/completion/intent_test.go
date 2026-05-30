package completion

import "testing"

func TestDetectIntent(t *testing.T) {
	tests := []struct {
		name     string
		buffer   string
		wantGoal string
		wantOK   bool
	}{
		{"hash prefix", "# push to github", "push to github", true},
		{"hash prefix trimmed", "#   compress this folder  ", "compress this folder", true},
		{"hash only is not intent", "#", "", false},
		{"i wanna lead-in", "i wanna push to github", "i wanna push to github", true},
		{"how do i lead-in", "How do I list docker images", "How do I list docker images", true},
		{"please lead-in", "please undo last commit", "please undo last commit", true},
		// normal commands must NOT be hijacked
		{"plain git command", "git status", "", false},
		{"make is a command not intent", "make build", "", false},
		{"command prefix", "doc", "", false},
		{"install command", "npm install", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goal, ok := detectIntent(tt.buffer)
			if ok != tt.wantOK || goal != tt.wantGoal {
				t.Errorf("detectIntent(%q) = (%q, %v), want (%q, %v)", tt.buffer, goal, ok, tt.wantGoal, tt.wantOK)
			}
		})
	}
}
