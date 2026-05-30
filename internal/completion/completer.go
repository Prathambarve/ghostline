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

func (c *Completer) Complete(buffer string, ctx *session.Context) (string, error) {
	if strings.TrimSpace(buffer) == "" {
		return "", nil
	}

	prompt := buildPrompt(buffer, ctx)

	tctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.timeoutMS)*time.Millisecond)
	defer cancel()

	response, err := c.gen.Generate(tctx, prompt, 80)
	if err != nil {
		return "", err
	}

	return sanitize(response, buffer), nil
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
	switch {
	case suffix == buffer:
		return "" // nothing added
	case strings.HasPrefix(suffix, buffer):
		suffix = suffix[len(buffer):]
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
		line = strings.Trim(line, "`\"'")
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
