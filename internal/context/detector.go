package context

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxDirFiles caps how many filenames we include to keep the prompt tight.
const maxDirFiles = 20

// maxGitStatusLines caps changed-file lines sent to the model.
const maxGitStatusLines = 10

type GitInfo struct {
	Branch string
	Repo   string
}

func DetectGit(cwd string) GitInfo {
	var info GitInfo

	out, err := exec.Command("git", "-C", cwd, "branch", "--show-current").Output()
	if err == nil {
		info.Branch = strings.TrimSpace(string(out))
	}

	out, err = exec.Command("git", "-C", cwd, "remote", "get-url", "origin").Output()
	if err == nil {
		remote := strings.TrimSpace(string(out))
		parts := strings.Split(strings.TrimSuffix(remote, ".git"), "/")
		if len(parts) > 0 {
			info.Repo = parts[len(parts)-1]
		}
	}

	return info
}

func DetectProject(cwd string) string {
	markers := []struct {
		file    string
		project string
	}{
		{"package.json", "node"},
		{"go.mod", "go"},
		{"requirements.txt", "python"},
		{"pyproject.toml", "python"},
		{"Cargo.toml", "rust"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"Gemfile", "ruby"},
	}

	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(cwd, m.file)); err == nil {
			return m.project
		}
	}

	entries, err := os.ReadDir(cwd)
	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tf") {
				return "terraform"
			}
		}
	}

	return "unknown"
}

// DetectGitStatus returns short-format git status lines for changed files,
// capped at maxGitStatusLines. Returns nil outside a git repo or on error.
func DetectGitStatus(cwd string) []string {
	out, err := exec.Command("git", "-C", cwd, "status", "--short").Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= maxGitStatusLines {
			break
		}
	}
	return lines
}

// DetectDirFiles returns the names of files and directories in cwd,
// capped at maxDirFiles. Hidden files (dotfiles) are excluded to reduce noise.
func DetectDirFiles(cwd string) []string {
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
		if len(names) >= maxDirFiles {
			break
		}
	}
	return names
}
