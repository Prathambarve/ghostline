package completion

import "testing"

func TestSanitize(t *testing.T) {
	tests := []struct {
		name     string
		response string
		buffer   string
		want     string
	}{
		{"suffix only", "eckout", "git ch", "git checkout"},
		{"suffix with leading/trailing space", "  eckout  ", "git ch", "git checkout"},
		{"model echoed full command", "git checkout", "git ch", "git checkout"},
		{"wrapped in code fence", "```\neckout\n```", "git ch", "git checkout"},
		{"wrapped in quotes", "\"eckout\"", "git ch", "git checkout"},
		{"wrapped in backticks", "`eckout`", "git ch", "git checkout"},
		{"multiline takes first line", "eckout\nthis checks out a branch", "git ch", "git checkout"},
		{"leading blank lines", "\n\n   eckout", "git ch", "git checkout"},
		{"empty response", "", "git ch", ""},
		{"whitespace-only response", "   \n  ", "git ch", ""},
		{"echoed buffer unchanged", "git ch", "git ch", ""},
		{"new arg after trailing space", "--oneline", "git log ", "git log --oneline"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitize(tt.response, tt.buffer); got != tt.want {
				t.Errorf("sanitize(%q, %q) = %q, want %q", tt.response, tt.buffer, got, tt.want)
			}
		})
	}
}
