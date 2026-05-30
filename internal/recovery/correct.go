package recovery

import "strings"

// commonCommands is a small allow-list of frequently-typed commands used as the
// target set for edit-distance typo correction.
var commonCommands = []string{
	"git", "ls", "cd", "cat", "grep", "find", "make", "go", "npm", "node",
	"python", "python3", "pip", "pip3", "docker", "kubectl", "ssh", "scp",
	"curl", "wget", "mkdir", "rm", "cp", "mv", "echo", "vim", "nano", "less",
	"brew", "cargo", "rustc", "java", "ruby", "sed", "awk", "tar", "ps",
	"kill", "clear", "exit", "sudo", "chmod", "chown", "touch", "head", "tail",
}

// knownTypos maps unambiguous, high-frequency misspellings straight to the
// intended command. These bypass edit-distance entirely.
var knownTypos = map[string]string{
	"gti": "git", "got": "git", "gut": "git",
	"sl": "ls", "lls": "ls", "lsl": "ls",
	"grpe": "grep", "gerp": "grep",
	"mkdr": "mkdir", "mkidr": "mkdir",
	"pyhton": "python", "pythn": "python",
	"dokcer": "docker", "dcoker": "docker",
	"claer": "clear", "clera": "clear",
	"exti": "exit", "eixt": "exit",
	"sudp": "sudo", "suod": "sudo",
	"cta": "cat",
}

// tryDeterministic fixes the simplest, unambiguous error — a single mistyped
// command name with no arguments — without calling the LLM. It returns a
// *Result on a confident fix, or nil to fall through to the model.
//
// It deliberately only handles the no-argument case: when arguments are present
// they may themselves contain typos (e.g. "gitt stauts" → the user wants
// "git status", not "git stauts"), and only the LLM can correct the whole line.
// Single-token typos, by contrast, are high-confidence and worth doing instantly
// on every backend.
func tryDeterministic(cmd string, exitCode int, stderr string) *Result {
	// Only act on "command not found" situations to avoid clobbering commands
	// that failed for real (a valid command with a runtime error).
	lower := strings.ToLower(stderr)
	notFound := exitCode == 127 ||
		strings.Contains(lower, "command not found") ||
		strings.Contains(lower, "not found")
	if !notFound {
		return nil
	}

	fields := strings.Fields(cmd)
	if len(fields) != 1 {
		// Multi-token (has arguments) → let the LLM correct the full line.
		return nil
	}
	name := fields[0]

	corrected := correctToken(name)
	if corrected == "" || corrected == name {
		return nil
	}

	return &Result{
		Fix: corrected,
		Why: "\"" + name + "\" isn't a command — did you mean \"" + corrected + "\"?",
	}
}

// correctToken returns the intended command for a mistyped one, or "" if it
// can't confidently correct it.
func correctToken(tok string) string {
	if to, ok := knownTypos[tok]; ok {
		return to
	}
	// Edit-distance-1 against the common set — accept only a unique match so we
	// never guess between two equally-plausible commands.
	var match string
	for _, c := range commonCommands {
		if c == tok {
			return "" // already a real command; nothing to fix
		}
		if editDistanceWithinOne(tok, c) {
			if match != "" {
				return "" // ambiguous
			}
			match = c
		}
	}
	return match
}

// editDistanceWithinOne reports whether a and b are at most one single-character
// insertion, deletion, or substitution apart.
func editDistanceWithinOne(a, b string) bool {
	la, lb := len(a), len(b)
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	if la == lb {
		diff := 0
		for i := 0; i < la; i++ {
			if a[i] != b[i] {
				diff++
				if diff > 1 {
					return false
				}
			}
		}
		return diff == 1
	}
	// Lengths differ by exactly one: check for a single insertion/deletion.
	if la > lb {
		a, b = b, a // ensure a is the shorter
	}
	i, j, edits := 0, 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		j++ // skip the extra char in the longer string
	}
	return true
}
