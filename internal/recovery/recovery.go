package recovery

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prathamesh/ghostline/internal/session"
)

// indexDash returns the byte offset and width of the first Unicode dash rune in
// s (em, en, figure, horizontal-bar, or minus-sign), or (-1, 0) if none. ASCII
// hyphen is deliberately excluded — it's part of flags like -m and --oneline.
func indexDash(s string) (int, int) {
	for i, r := range s {
		switch r {
		case '‒', '–', '—', '―', '−':
			return i, utf8.RuneLen(r)
		}
	}
	return -1, 0
}

type generator interface {
	Generate(ctx context.Context, prompt string, maxTokens int) (string, error)
}

type Recovery struct {
	gen       generator
	timeoutMS int
}

type Result struct {
	Fix string
	Why string
}

func New(gen generator, timeoutMS int) *Recovery {
	return &Recovery{gen: gen, timeoutMS: timeoutMS}
}

func (r *Recovery) Recover(cmd string, exitCode int, stderr string, ctx *session.Context) (*Result, error) {
	// Tier 1: fast, deterministic, offline. Handles the common case (a mistyped
	// command) instantly and avoids an API round-trip.
	if res := tryDeterministic(cmd, exitCode, stderr); res != nil {
		return res, nil
	}

	// Tier 2: fall through to the model for anything the ruleset can't solve.
	prompt := buildPrompt(cmd, exitCode, stderr, ctx)

	tctx, cancel := context.WithTimeout(context.Background(), time.Duration(r.timeoutMS)*time.Millisecond)
	defer cancel()

	response, err := r.gen.Generate(tctx, prompt, 120)
	if err != nil {
		return nil, err
	}

	return parseResponse(response), nil
}

func parseResponse(response string) *Result {
	response = strings.TrimSpace(response)
	if response == "NONE" || response == "" {
		return nil
	}

	result := &Result{}
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "FIX: "); ok {
			result.Fix = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "WHY: "); ok {
			result.Why = strings.TrimSpace(after)
		}
	}

	if result.Fix == "" {
		return nil
	}

	// The FIX must be a runnable command (the shell pre-fills it into the buffer
	// to run on Enter). Models sometimes inline the explanation as
	// "command — reason"; a Unicode dash never appears in a real shell command, so
	// split it off and treat the tail as the WHY when one isn't already present.
	if i, size := indexDash(result.Fix); i >= 0 {
		tail := strings.TrimSpace(result.Fix[i+size:])
		result.Fix = strings.TrimSpace(result.Fix[:i])
		if result.Why == "" {
			result.Why = tail
		}
	}

	if result.Fix == "" {
		return nil
	}
	return result
}
