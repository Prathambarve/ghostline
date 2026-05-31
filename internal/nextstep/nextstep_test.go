package nextstep

import (
	"strings"
	"testing"

	"github.com/prathamesh/ghostline/internal/session"
)

func TestShouldPredict(t *testing.T) {
	tests := []struct {
		cmd  string
		exit int
		want bool
	}{
		{"terraform plan", 0, true}, // workflow command
		{"git add -A", 0, true},     // workflow command
		{"docker build -t app .", 0, true},
		{"npm install", 0, true},
		{"ls", 0, false},                // trivial
		{"cd /tmp", 0, false},           // trivial
		{"cat file.txt", 0, false},      // trivial
		{"cat missing.txt", 1, true},    // failure → predict recovery/rollback
		{"some-random-binary", 1, true}, // failure
		{"ghostline status", 0, false},  // never predict on our own commands
		{"", 0, false},
	}
	for _, tt := range tests {
		if got := ShouldPredict(tt.cmd, tt.exit); got != tt.want {
			t.Errorf("ShouldPredict(%q, %d) = %v, want %v", tt.cmd, tt.exit, got, tt.want)
		}
	}
}

func TestIsDestructive(t *testing.T) {
	destructive := []string{
		"terraform apply", "terraform destroy", "git push --force",
		"git push -f origin main", "rm -rf build", "kubectl delete pod x",
		"git reset --hard HEAD~1", "helm uninstall app", "npm run deploy",
	}
	for _, c := range destructive {
		if !isDestructive(c) {
			t.Errorf("isDestructive(%q) = false, want true", c)
		}
	}
	safe := []string{
		"terraform plan", "git status", "cd app && npm install",
		"git commit -m msg", "docker run --rm -it app", "ls -la",
	}
	for _, c := range safe {
		if isDestructive(c) {
			t.Errorf("isDestructive(%q) = true, want false", c)
		}
	}
}

func TestParseResult(t *testing.T) {
	tests := []struct {
		name            string
		resp            string
		wantNil         bool
		wantNext        string
		wantDestructive bool
	}{
		{"safe next", "NEXT: git commit\nRISK: safe", false, "git commit", false},
		{"destructive flagged by model", "NEXT: terraform apply\nRISK: destructive", false, "terraform apply", true},
		{"destructive caught by pattern despite safe", "NEXT: git push --force\nRISK: safe", false, "git push --force", true},
		{"NONE", "NONE", true, "", false},
		{"empty", "", true, "", false},
		{"next NONE", "NEXT: NONE\nRISK: safe", true, "", false},
		{"whitespace", "  NEXT:  npm test \n RISK: safe ", false, "npm test", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseResult(tt.resp)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("parseResult(%q) = %+v, want nil", tt.resp, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseResult(%q) = nil, want %q", tt.resp, tt.wantNext)
			}
			if got.Next != tt.wantNext || got.Destructive != tt.wantDestructive {
				t.Errorf("parseResult(%q) = {Next:%q Destructive:%v}, want {Next:%q Destructive:%v}",
					tt.resp, got.Next, got.Destructive, tt.wantNext, tt.wantDestructive)
			}
		})
	}
}

func TestBuildPromptIncludesTrajectory(t *testing.T) {
	ctx := &session.Context{
		CWD:            "/tmp/app",
		GitRepo:        "app",
		RecentCommands: []session.CmdRecord{{Command: "git add -A"}, {Command: "git status"}},
	}
	p := buildPrompt("terraform plan", 0, ctx)
	for _, want := range []string{"terraform plan", "Recent commands:", "git add -A", "NEXT:", "RISK:"} {
		if !strings.Contains(p, want) {
			t.Errorf("buildPrompt missing %q:\n%s", want, p)
		}
	}
}
