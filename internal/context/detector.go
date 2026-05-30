package context

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
