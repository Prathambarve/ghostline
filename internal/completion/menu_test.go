package completion

import "testing"

func TestParseMenuParsesCommandsAndDescriptions(t *testing.T) {
	resp := "git checkout ||| switch branches\n" +
		"git cherry-pick ||| apply a commit\n" +
		"git cherry ||| find unmerged commits\n"

	got := parseMenu(resp)
	if len(got) != 3 {
		t.Fatalf("expected 3 candidates, got %d: %+v", len(got), got)
	}
	if got[0].Command != "git checkout" || got[0].Description != "switch branches" {
		t.Errorf("first candidate wrong: %+v", got[0])
	}
}

func TestParseMenuStripsFencesAndQuotes(t *testing.T) {
	resp := "```\n\"git status\" ||| show working tree\n```"
	got := parseMenu(resp)
	if len(got) != 1 || got[0].Command != "git status" {
		t.Fatalf("expected fences/quotes stripped, got %+v", got)
	}
}

func TestParseMenuHandlesMissingDescription(t *testing.T) {
	got := parseMenu("go build ./...\n")
	if len(got) != 1 || got[0].Command != "go build ./..." || got[0].Description != "" {
		t.Fatalf("expected a command with empty description, got %+v", got)
	}
}

func TestParseMenuDedupesAndCaps(t *testing.T) {
	resp := "ls ||| a\nls ||| b\nls -l ||| c\nls -la ||| d\npwd ||| e\ncd .. ||| f\ndf ||| g\ndu ||| h\n"
	got := parseMenu(resp)
	if len(got) > maxMenuCandidates {
		t.Fatalf("expected at most %d candidates, got %d", maxMenuCandidates, len(got))
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.Command] {
			t.Errorf("duplicate command %q in output", c.Command)
		}
		seen[c.Command] = true
	}
}

func TestParseMenuEmpty(t *testing.T) {
	if got := parseMenu("   \n\n"); got != nil {
		t.Errorf("expected nil for empty response, got %+v", got)
	}
}
