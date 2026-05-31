// Package envprobe gathers environment facts about the tool a failed command
// invoked — which binary is on PATH, its installed version, version-manager pins
// declared in the project, and the active virtualenv. These facts let the
// recovery model diagnose shell/environment failures precisely (e.g. "python
// 3.13 is active but .python-version pins 3.10") instead of guessing.
//
// It is designed to run client-side, inside the `ghostline recover` process,
// which is a direct child of the interactive shell and so inherits the shell's
// real PATH and $VIRTUAL_ENV — the daemon, being detached, would not. Probe
// output is only ever placed into the in-flight prompt; it is never written to
// disk.
package envprobe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// budget bounds the single `<tool> --version` subprocess so recovery never
// stalls on a slow or hanging binary; on timeout we send whatever we gathered.
const budget = 300 * time.Millisecond

// Facts holds environment signals relevant to a failed command's tool. Every
// field is best-effort and may be empty.
type Facts struct {
	Tool     string   // the command's first token, e.g. "python"
	Resolved string   // `which <tool>` result; "" if not on PATH
	Version  string   // first line of `<tool> --version`; "" if unknown
	Declared []string // version-manager pins found in cwd, e.g. ".python-version: 3.10"
	Venv     string   // basename of $VIRTUAL_ENV, or ""
	// FileMode describes a path-form command's target file (e.g. "./deploy.sh"):
	// whether it exists, is a directory, and whether it is executable. Set only
	// when the tool token is a path; empty otherwise.
	FileMode string
}

// Seams overridden in tests so they don't depend on the host's real toolchain.
var (
	lookPath   = exec.LookPath
	statFn     = os.Stat
	execRunner = func(ctx context.Context, name string, args ...string) (string, bool) {
		// CombinedOutput: some tools (notably `java -version`) print to stderr.
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}
)

// versionFlag is the curated allow-list of tools we will exec `--version` on.
// Unknown tools are skipped to avoid hanging on a binary with no version flag.
var versionFlag = map[string]string{
	"python": "--version", "python3": "--version",
	"node": "--version", "go": "version", "ruby": "--version",
	"java": "-version", "npm": "--version", "pip": "--version",
	"pip3": "--version", "cargo": "--version", "rustc": "--version",
	"deno": "--version", "bun": "--version", "php": "--version", "perl": "--version",
}

// toolVersionFiles maps a tool to the version-manager files worth reading in the
// project directory.
var toolVersionFiles = map[string][]string{
	"python":  {".python-version", ".tool-versions"},
	"python3": {".python-version", ".tool-versions"},
	"pip":     {".python-version", ".tool-versions"},
	"pip3":    {".python-version", ".tool-versions"},
	"node":    {".nvmrc", ".node-version", ".tool-versions"},
	"npm":     {".nvmrc", ".node-version", ".tool-versions"},
	"npx":     {".nvmrc", ".node-version", ".tool-versions"},
	"yarn":    {".nvmrc", ".node-version", ".tool-versions"},
	"go":      {"go.mod", ".tool-versions"},
	"ruby":    {".ruby-version", ".tool-versions"},
	"bundle":  {".ruby-version", ".tool-versions"},
	"gem":     {".ruby-version", ".tool-versions"},
	"java":    {".java-version", ".tool-versions"},
	"cargo":   {"rust-toolchain.toml", ".tool-versions"},
	"rustc":   {"rust-toolchain.toml", ".tool-versions"},
}

// asdfName maps a tool to its canonical name in an asdf .tool-versions file.
var asdfName = map[string]string{
	"python": "python", "python3": "python", "pip": "python", "pip3": "python",
	"node": "nodejs", "npm": "nodejs", "npx": "nodejs", "yarn": "nodejs",
	"go": "golang", "ruby": "ruby", "bundle": "ruby", "gem": "ruby",
	"java": "java", "cargo": "rust", "rustc": "rust",
}

// Probe gathers environment facts about the tool invoked by cmd, reading version
// files relative to cwd.
func Probe(cmd, cwd string) Facts {
	tool := firstToken(cmd)
	f := Facts{Tool: tool}
	if tool == "" {
		return f
	}

	// Cheap, instant signals.
	if v := os.Getenv("VIRTUAL_ENV"); v != "" {
		f.Venv = filepath.Base(v)
	}
	if cwd != "" {
		f.Declared = declaredVersions(tool, cwd)
	}

	// A command given as a path ("./deploy.sh", "bin/run") is a local file, not
	// an installable PATH binary — diagnose its file mode (the common exit-126
	// "permission denied" case) rather than suggesting a package manager.
	if strings.Contains(tool, "/") {
		f.FileMode = statFile(tool, cwd)
		return f
	}

	// PATH resolution is fast and needs no subprocess.
	if resolved, err := lookPath(tool); err == nil {
		f.Resolved = resolved
		// Only pay for the `--version` exec when there's a declared pin to
		// compare against — that's the one case where the installed version adds
		// signal (a mismatch). Without a pin it's cost for no diagnostic value.
		if len(f.Declared) > 0 {
			if flag, ok := versionFlag[tool]; ok {
				ctx, cancel := context.WithTimeout(context.Background(), budget)
				defer cancel()
				if out, ok := execRunner(ctx, tool, flag); ok {
					f.Version = firstNonEmptyLine(out)
				}
			}
		}
	}
	// When the tool is not on PATH we report only that it is missing (see Prompt).
	// We deliberately do NOT suggest a package manager: an install for what may be
	// a typo can't be verified offline and erodes trust.

	return f
}

// statFile reports the state of a path-form command's target file.
func statFile(tool, cwd string) string {
	path := tool
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, tool)
	}
	info, err := statFn(path)
	if err != nil {
		return "does not exist"
	}
	if info.IsDir() {
		return "is a directory, not an executable"
	}
	if info.Mode()&0111 == 0 {
		return "exists but is not executable"
	}
	return "is an executable file"
}

// Prompt renders the facts as a compact block for the recovery prompt, or "" if
// there is nothing useful to report.
func (f Facts) Prompt() string {
	if f.Tool == "" {
		return ""
	}
	var sb strings.Builder
	wrote := false
	w := func(format string, args ...any) {
		if !wrote {
			sb.WriteString("Environment:\n")
			wrote = true
		}
		fmt.Fprintf(&sb, format, args...)
	}

	switch {
	case f.FileMode != "":
		w("- %s %s\n", f.Tool, f.FileMode)
	case f.Resolved == "":
		w("- %q is not on PATH\n", f.Tool)
	default:
		w("- %s resolves to %s\n", f.Tool, f.Resolved)
		if f.Version != "" {
			w("- installed version: %s\n", f.Version)
		}
	}
	if f.Venv != "" {
		w("- active virtualenv: %s\n", f.Venv)
	}
	for _, d := range f.Declared {
		w("- project pins %s\n", d)
	}
	return sb.String()
}

// declaredVersions reads the version-manager pins relevant to tool from cwd.
func declaredVersions(tool, cwd string) []string {
	var out []string
	for _, file := range toolVersionFiles[tool] {
		data, err := os.ReadFile(filepath.Join(cwd, file))
		if err != nil {
			continue
		}
		if v := parsePin(tool, file, string(data)); v != "" {
			out = append(out, file+": "+v)
		}
	}
	return out
}

// parsePin extracts the pinned version for tool from a version file's content.
func parsePin(tool, file, content string) string {
	switch file {
	case "go.mod":
		// the "go 1.x" directive
		for _, line := range strings.Split(content, "\n") {
			if after, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
				return strings.TrimSpace(after)
			}
		}
		return ""
	case ".tool-versions":
		// asdf: lines like "python 3.10.0"
		for _, line := range strings.Split(content, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && (asdfName[tool] == fields[0] || tool == fields[0]) {
				return fields[1]
			}
		}
		return ""
	case "rust-toolchain.toml":
		for _, line := range strings.Split(content, "\n") {
			if after, ok := strings.CutPrefix(strings.TrimSpace(line), "channel"); ok {
				return strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(after), "=")), `"`)
			}
		}
		return ""
	default:
		// raw single-value files (.python-version, .nvmrc, .node-version, ...)
		return firstNonEmptyLine(content)
	}
}

// firstToken returns the command's tool name, skipping leading VAR=value
// environment assignments (e.g. "FOO=bar python x" → "python").
func firstToken(cmd string) string {
	for _, f := range strings.Fields(cmd) {
		if !strings.HasPrefix(f, "-") && strings.Contains(f, "=") {
			continue
		}
		return f
	}
	return ""
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
