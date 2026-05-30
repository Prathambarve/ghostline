package completion

import (
	"context"
	"strings"
	"time"

	"github.com/prathamesh/ghostline/internal/session"
)

type generator interface {
	Generate(ctx context.Context, prompt string, maxTokens int) (string, error)
}

type Completer struct {
	gen       generator
	timeoutMS int
}

func New(gen generator, timeoutMS int) *Completer {
	return &Completer{gen: gen, timeoutMS: timeoutMS}
}

func (c *Completer) Complete(buffer string, ctx *session.Context, frequent, successors []string) (string, error) {
	if strings.TrimSpace(buffer) == "" {
		return "", nil
	}

	// Intent mode (safe single-step): the user described a goal in plain words
	// rather than typing a command prefix. We return ONE runnable command that
	// replaces the buffer — the user still reads it and presses Enter. We never
	// chain or auto-run; the next step is suggested only after they run this one.
	if goal, ok := detectIntent(buffer); ok {
		return c.completeIntent(goal, ctx, frequent, successors)
	}

	prompt := buildPrompt(buffer, ctx, frequent, successors)

	tctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.timeoutMS)*time.Millisecond)
	defer cancel()

	response, err := c.gen.Generate(tctx, prompt, 80)
	if err != nil {
		return "", err
	}

	return sanitize(response, buffer), nil
}

func (c *Completer) completeIntent(goal string, ctx *session.Context, frequent, successors []string) (string, error) {
	prompt := buildIntentPrompt(goal, ctx, frequent, successors)

	tctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.timeoutMS)*time.Millisecond)
	defer cancel()

	response, err := c.gen.Generate(tctx, prompt, 80)
	if err != nil {
		return "", err
	}

	// Intent responses are a full command, not a suffix — return it verbatim
	// (sans fences/quotes) so the widget replaces the buffer with it.
	return firstMeaningfulLine(response), nil
}

// intentLeadIns are phrases no real shell command begins with, so matching them
// can't hijack a normal completion. The explicit "#" prefix is handled in
// detectIntent.
var intentLeadIns = []string{
	"i want ", "i wanna ", "i need ", "i'd like ",
	"how do i ", "how to ", "please ",
}

// detectIntent reports whether buffer is a natural-language goal rather than a
// command prefix, returning the goal text to translate.
func detectIntent(buffer string) (string, bool) {
	trimmed := strings.TrimSpace(buffer)
	if strings.HasPrefix(trimmed, "#") {
		goal := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		return goal, goal != ""
	}
	lower := strings.ToLower(trimmed)
	for _, lead := range intentLeadIns {
		if strings.HasPrefix(lower, lead) {
			return trimmed, true
		}
	}
	return "", false
}

// sanitize turns a raw model response into the full completed command, or "" if
// there's nothing useful to suggest. The model is asked to return only the
// suffix (text after the buffer), but we defensively handle models that echo
// the whole command and strip code fences / quotes / stray whitespace.
func sanitize(response, buffer string) string {
	suffix := firstMeaningfulLine(response)
	if suffix == "" {
		return ""
	}

	// Defensive: some models echo the full command instead of just the suffix.
	// Detect that and convert back to a pure suffix so we never double the prefix.
	// We also normalise away a trailing space on the buffer before comparing, so
	// "git log " + echo "git log" doesn't produce "git log git log".
	bufferTrimmed := strings.TrimRight(buffer, " \t")
	switch {
	case suffix == buffer:
		return "" // nothing added
	case suffix == bufferTrimmed:
		return "" // model echoed command without the trailing space — nothing new
	case strings.HasPrefix(suffix, buffer):
		suffix = suffix[len(buffer):]
	case bufferTrimmed != buffer && strings.HasPrefix(suffix, bufferTrimmed):
		// Buffer had a trailing space; model returned the base without it then
		// added content — e.g. buffer="git log " suffix="git log --oneline".
		suffix = suffix[len(bufferTrimmed):]
		suffix = strings.TrimLeft(suffix, " \t")
	}

	if suffix == "" {
		return ""
	}
	return buffer + suffix
}

func firstMeaningfulLine(response string) string {
	// Drop a leading fenced block delimiter if the model wrapped output in ```.
	response = strings.ReplaceAll(response, "```", "")
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		line = stripWrappingQuotes(line)
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// stripWrappingQuotes removes a matching pair of wrapping single or double
// quotes from a line — e.g. `"eckout"` → `eckout`. Backticks are deliberately
// NOT stripped: in shell they denote command substitution and must be preserved
// (e.g. `echo \`date\`` must remain intact after completion).
func stripWrappingQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	if (s[0] == '"' && s[len(s)-1] == '"') ||
		(s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
