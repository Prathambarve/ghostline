package recovery

import (
	"strings"
	"testing"

	"github.com/prathamesh/ghostline/internal/session"
)

func TestBuildPromptIncludesEnvAndScope(t *testing.T) {
	env := "Environment:\n- python resolves to /usr/bin/python\n- installed version: Python 3.13.1\n- project pins .python-version: 3.10\n"
	got := buildPrompt("python app.py", 1, "boom", env, &session.Context{CWD: "/tmp/proj"})

	// The environment facts must reach the model verbatim.
	if !strings.Contains(got, "project pins .python-version: 3.10") {
		t.Errorf("prompt missing env block:\n%s", got)
	}
	// The scope rule must instruct the model to bow out of source-code bugs.
	if !strings.Contains(got, "NONE") || !strings.Contains(got, "source code") {
		t.Errorf("prompt missing source-code scope rule:\n%s", got)
	}
}

func TestBuildPromptOmitsEmptyEnv(t *testing.T) {
	got := buildPrompt("gitt status", 127, "command not found: gitt", "", &session.Context{})
	if strings.Contains(got, "Environment:") {
		t.Errorf("prompt should not contain an env block when none was probed:\n%s", got)
	}
}
