package envprobe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstToken(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"python app.py", "python"},
		{"  go   build ./...", "go"},
		{"FOO=bar BAZ=qux python x", "python"},
		{"--flag", "--flag"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		if got := firstToken(tt.cmd); got != tt.want {
			t.Errorf("firstToken(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func TestParsePin(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		file    string
		content string
		want    string
	}{
		{"python-version raw", "python", ".python-version", "3.10.4\n", "3.10.4"},
		{"nvmrc raw", "node", ".nvmrc", "v18.16.0", "v18.16.0"},
		{"go.mod directive", "go", "go.mod", "module x\n\ngo 1.21\n\nrequire y\n", "1.21"},
		{"tool-versions asdf match", "python", ".tool-versions", "nodejs 18.0.0\npython 3.11.2\n", "3.11.2"},
		{"tool-versions no match", "ruby", ".tool-versions", "python 3.11.2\n", ""},
		{"rust-toolchain channel", "cargo", "rust-toolchain.toml", "[toolchain]\nchannel = \"1.72.0\"\n", "1.72.0"},
		{"empty raw file", "python", ".python-version", "\n  \n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePin(tt.tool, tt.file, tt.content); got != tt.want {
				t.Errorf("parsePin(%q,%q,...) = %q, want %q", tt.tool, tt.file, got, tt.want)
			}
		})
	}
}

func TestProbeVersionMismatch(t *testing.T) {
	// Fake a host where `python` is on PATH at 3.13, while the project pins 3.10.
	defer restoreSeams()
	lookPath = func(name string) (string, error) {
		if name == "python" {
			return "/usr/bin/python", nil
		}
		return "", errors.New("not found")
	}
	execRunner = func(ctx context.Context, name string, args ...string) (string, bool) {
		return "Python 3.13.1", true
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".python-version"), []byte("3.10\n"), 0600); err != nil {
		t.Fatal(err)
	}

	f := Probe("python app.py", dir)
	if f.Resolved != "/usr/bin/python" {
		t.Errorf("Resolved = %q, want /usr/bin/python", f.Resolved)
	}
	if f.Version != "Python 3.13.1" {
		t.Errorf("Version = %q, want Python 3.13.1", f.Version)
	}
	if len(f.Declared) != 1 || f.Declared[0] != ".python-version: 3.10" {
		t.Errorf("Declared = %v, want [.python-version: 3.10]", f.Declared)
	}

	prompt := f.Prompt()
	for _, want := range []string{"resolves to /usr/bin/python", "Python 3.13.1", "project pins .python-version: 3.10"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("Prompt() missing %q:\n%s", want, prompt)
		}
	}
}

func TestProbeNotFoundReportsMissingOnly(t *testing.T) {
	defer restoreSeams()
	lookPath = func(name string) (string, error) {
		return "", errors.New("not found")
	}
	execRunner = func(ctx context.Context, name string, args ...string) (string, bool) {
		t.Fatalf("execRunner must not run for a tool that is not on PATH (called for %q)", name)
		return "", false
	}

	f := Probe("htop", t.TempDir())
	if f.Resolved != "" {
		t.Errorf("Resolved = %q, want empty", f.Resolved)
	}
	prompt := f.Prompt()
	if !strings.Contains(prompt, "is not on PATH") {
		t.Errorf("Prompt() should report the tool is missing:\n%s", prompt)
	}
	// We must NOT nudge toward installing a possibly-typo'd command.
	if strings.Contains(strings.ToLower(prompt), "package manager") || strings.Contains(strings.ToLower(prompt), "install") {
		t.Errorf("Prompt() must not suggest installing a missing tool:\n%s", prompt)
	}
}

func TestProbePathNotExecutable(t *testing.T) {
	defer restoreSeams()
	lookPath = func(name string) (string, error) {
		t.Fatalf("lookPath must not run for a path-form command (called for %q)", name)
		return "", nil
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}

	f := Probe("./deploy.sh arg", dir)
	if f.FileMode != "exists but is not executable" {
		t.Errorf("FileMode = %q, want \"exists but is not executable\"", f.FileMode)
	}
	if f.Resolved != "" {
		t.Errorf("path-form command must not resolve as a PATH binary: %+v", f)
	}
	prompt := f.Prompt()
	if !strings.Contains(prompt, "is not executable") || strings.Contains(prompt, "not on PATH") {
		t.Errorf("Prompt() should report file mode, not a PATH miss:\n%s", prompt)
	}
}

func TestProbePathExecutable(t *testing.T) {
	defer restoreSeams()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := Probe("./run", dir).FileMode; got != "is an executable file" {
		t.Errorf("FileMode = %q, want \"is an executable file\"", got)
	}
}

func TestProbeVersionSkippedWithoutPin(t *testing.T) {
	// python resolves, but with no project pin there is nothing to compare the
	// installed version against, so the version exec must be skipped.
	defer restoreSeams()
	lookPath = func(name string) (string, error) { return "/usr/bin/python", nil }
	execRunner = func(ctx context.Context, name string, args ...string) (string, bool) {
		t.Fatalf("version exec must be skipped when no pin is declared (called for %q)", name)
		return "", false
	}

	f := Probe("python app.py", t.TempDir()) // empty dir → no pins
	if f.Version != "" {
		t.Errorf("Version = %q, want empty (no pin → no exec)", f.Version)
	}
}

func TestProbeEmptyCommand(t *testing.T) {
	if got := Probe("", "").Prompt(); got != "" {
		t.Errorf("Prompt() for empty command = %q, want empty", got)
	}
}

func restoreSeams() {
	lookPath = origLookPath
	execRunner = origExecRunner
	statFn = origStatFn
}

var (
	origLookPath   = lookPath
	origExecRunner = execRunner
	origStatFn     = statFn
)
