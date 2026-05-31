package daemon

import "testing"

func TestLooksLikeCorrection(t *testing.T) {
	tests := []struct {
		failed string
		fixed  string
		want   bool
	}{
		// Genuine corrections.
		{"clde", "claude", true},
		{"gti", "git", true},
		{"dokcer", "docker", true},                // adjacent transposition
		{"python3 app.py", "python app.py", true}, // wrong interpreter
		{"ls -la", "ls -al", true},                // flag transposition

		// Not corrections — different commands typed back-to-back.
		{"ls", "cd", false}, // too short to tell apart
		{"git status", "git push", false},
		{"npm install", "npm run dev", false},
		{"cat a.txt", "rm a.txt", false},

		// Degenerate.
		{"git", "git", false}, // identical is not a correction
		{"", "git", false},
		{"git", "", false},
	}
	for _, tt := range tests {
		if got := looksLikeCorrection(tt.failed, tt.fixed); got != tt.want {
			t.Errorf("looksLikeCorrection(%q, %q) = %v, want %v (osa=%d)",
				tt.failed, tt.fixed, got, tt.want, osaDistance(tt.failed, tt.fixed))
		}
	}
}

func TestOSADistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1}, // substitution
		{"abc", "ab", 1},  // deletion
		{"ab", "abc", 1},  // insertion
		{"ab", "ba", 1},   // adjacent transposition (1 in OSA, 2 in plain Levenshtein)
		{"clde", "claude", 2},
	}
	for _, tt := range tests {
		if got := osaDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("osaDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
