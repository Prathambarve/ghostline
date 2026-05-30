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
	// Source identifies which tier produced the fix: "deterministic" (offline
	// typo corrector) or "llm" (model). It lets callers decide whether a fix is
	// worth caching — only LLM fixes are, since deterministic ones are already
	// instant.
	Source string
}

func New(gen generator, timeoutMS int) *Recovery {
	return &Recovery{gen: gen, timeoutMS: timeoutMS}
}

func (r *Recovery) Recover(cmd string, exitCode int, stderr string, ctx *session.Context) (*Result, error) {
	// Tier 1: fast, deterministic, offline. Corrects an unambiguous single-token
	// typo (`gti` → `git`) instantly on every backend, with no API round-trip —
	// these are high-confidence and can't really be wrong. Multi-token lines fall
	// through so the LLM can correct the whole line, including mistyped arguments.
	if res := tryDeterministic(cmd, exitCode, stderr); res != nil {
		res.Source = "deterministic"
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

	res := parseResponse(response)
	if res != nil {
		res.Source = "llm"
	}
	return res, nil
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

	// Models sometimes inline the explanation as "command — reason". Split on a
	// Unicode dash only when it's surrounded by spaces — that's the separator
	// usage. A dash with no surrounding spaces is part of a filename or argument
	// (e.g. "cat notes—2024.txt") and must not be touched.
	if i, size := indexDash(result.Fix); i >= 0 {
		before := result.Fix[:i]
		after := result.Fix[i+size:]
		if strings.HasSuffix(before, " ") && strings.HasPrefix(after, " ") {
			tail := strings.TrimSpace(after)
			result.Fix = strings.TrimSpace(before)
			if result.Why == "" {
				result.Why = tail
			}
		}
	}

	if result.Fix == "" {
		return nil
	}
	// A fix that begins with a Unicode dash is not a runnable command.
	if i, _ := indexDash(result.Fix); i == 0 {
		return nil
	}
	return result
}
