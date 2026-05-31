package completion

import (
	"context"
	"strings"
	"time"

	"github.com/prathamesh/ghostline/internal/session"
)

// Candidate is one selectable suggestion in the Fig-style completion menu: a
// full command line plus a short description. JSON tags let the daemon return
// these directly on the wire.
type Candidate struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

// menuDelimiter separates a command from its description in the model's output.
// Chosen to be vanishingly unlikely to appear inside a real shell command.
const menuDelimiter = "|||"

// maxMenuCandidates caps how many completions the menu offers.
const maxMenuCandidates = 5

// CompleteMenu returns several candidate completions for the buffer, each with a
// short description — the "Fig magic". Unlike Complete (a single instant suffix
// insert), this asks the model for a ranked set the user picks from. Returns nil
// on an empty buffer or when nothing useful comes back.
func (c *Completer) CompleteMenu(buffer string, ctx *session.Context, frequent, successors []string) ([]Candidate, error) {
	if strings.TrimSpace(buffer) == "" {
		return nil, nil
	}

	prompt := buildMenuPrompt(buffer, ctx, frequent, successors)

	tctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.timeoutMS)*time.Millisecond)
	defer cancel()

	// Larger token budget than single completion (80): several lines, each a
	// full command plus a short description.
	response, err := c.gen.Generate(tctx, prompt, 220)
	if err != nil {
		return nil, err
	}

	return parseMenu(response), nil
}

// parseMenu turns the model's "command ||| description" lines into candidates,
// stripping fences/quotes, dropping malformed or duplicate commands, and capping
// the count.
func parseMenu(response string) []Candidate {
	response = strings.ReplaceAll(response, "```", "")

	var out []Candidate
	seen := make(map[string]bool)
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		cmd, desc := line, ""
		if i := strings.Index(line, menuDelimiter); i >= 0 {
			cmd = strings.TrimSpace(line[:i])
			desc = strings.TrimSpace(line[i+len(menuDelimiter):])
		}
		cmd = strings.TrimSpace(stripWrappingQuotes(strings.TrimSpace(cmd)))
		if cmd == "" || seen[cmd] {
			continue
		}
		seen[cmd] = true
		out = append(out, Candidate{Command: cmd, Description: desc})
		if len(out) >= maxMenuCandidates {
			break
		}
	}
	return out
}
