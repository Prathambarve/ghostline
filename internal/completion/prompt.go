package completion

import (
	"fmt"
	"strings"

	"github.com/prathamesh/ghostline/internal/session"
)

// SamplePrompt returns a representative completion prompt for benchmarking, so
// latency measurements reflect a realistic payload size. MaxTokens mirrors the
// 80-token cap used by Complete.
func SamplePrompt() (prompt string, maxTokens int) {
	ctx := &session.Context{
		CWD:         "/Users/dev/project",
		GitBranch:   "main",
		GitRepo:     "ghostline",
		ProjectType: "go",
		RecentCommands: []session.CmdRecord{
			{Command: "git status"}, {Command: "go build ./..."}, {Command: "git add -A"},
		},
	}
	return buildPrompt("git ch", ctx, nil, []string{"git commit -m", "git push"}), 80
}

// buildIntentPrompt asks the model to turn a plain-language goal into a single
// runnable command. Deliberately one command only — Ghostline fills the buffer
// and the user presses Enter; we never chain or auto-run.
func buildIntentPrompt(goal string, ctx *session.Context, frequent, successors []string) string {
	var sb strings.Builder

	sb.WriteString("Translate this request into ONE runnable shell command for the current context.\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Output ONLY the command — no explanation, no markdown, no code fences, no quotes\n")
	sb.WriteString("- Exactly one command on a single line (no '&&' chains, no multiple lines)\n")
	sb.WriteString("- If you cannot map it to a safe single command, output nothing\n\n")

	writeContext(&sb, ctx, frequent, successors)

	sb.WriteString(fmt.Sprintf("\nRequest: %s\n", goal))
	sb.WriteString("Command:")
	return sb.String()
}

// buildMenuPrompt asks the model for several candidate completions, each with a
// short description — the data behind the Fig-style picker. Output is one
// candidate per line as "<full command> ||| <short description>".
func buildMenuPrompt(buffer string, ctx *session.Context, frequent, successors []string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Suggest up to %d likely completions of this shell command, best first.\n\n", maxMenuCandidates))
	sb.WriteString("Rules:\n")
	sb.WriteString("- Each line: the FULL command line (including the input prefix) ||| a short description (<= 6 words)\n")
	sb.WriteString("- Use ' ||| ' (three pipes) as the separator, exactly once per line\n")
	sb.WriteString("- One candidate per line, no numbering, no markdown, no code fences, no quotes\n")
	sb.WriteString("- Each command must begin with the input and be runnable as-is\n")
	sb.WriteString("- If you cannot suggest anything useful, output nothing\n\n")
	sb.WriteString("Example — input \"git ch\":\n")
	sb.WriteString("git checkout ||| switch branches or restore files\n")
	sb.WriteString("git cherry-pick ||| apply commits from elsewhere\n\n")

	writeContext(&sb, ctx, frequent, successors)

	sb.WriteString(fmt.Sprintf("\nInput: %s\n", buffer))
	sb.WriteString("Candidates:\n")
	return sb.String()
}

func buildPrompt(buffer string, ctx *session.Context, frequent, successors []string) string {
	var sb strings.Builder

	sb.WriteString("Complete this shell command. Output ONLY the continuation — the text that comes AFTER the input, not the input itself.\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Do NOT repeat the input; output only the missing suffix\n")
	sb.WriteString("- No explanation, no markdown, no code fences, no quotes\n")
	sb.WriteString("- A single line; if you cannot complete it meaningfully, output nothing\n\n")
	sb.WriteString("Example — input \"git ch\" → output \"eckout\" (so the full command becomes \"git checkout\").\n\n")

	writeContext(&sb, ctx, frequent, successors)

	sb.WriteString(fmt.Sprintf("\nInput: %s\n", buffer))
	sb.WriteString("Continuation:")

	return sb.String()
}

// writeContext emits the shared "Context:" block used by both the completion
// and intent prompts. Includes cwd, git info, project type, directory listing,
// changed files, recent commands, cross-session frequent commands, and the
// likely next commands predicted from the command-transition model.
func writeContext(sb *strings.Builder, ctx *session.Context, frequent, successors []string) {
	sb.WriteString("Context:\n")
	if ctx.CWD != "" {
		sb.WriteString(fmt.Sprintf("- Directory: %s\n", ctx.CWD))
	}
	if ctx.GitBranch != "" {
		if ctx.GitRepo != "" {
			sb.WriteString(fmt.Sprintf("- Git: branch=%s repo=%s\n", ctx.GitBranch, ctx.GitRepo))
		} else {
			sb.WriteString(fmt.Sprintf("- Git branch: %s\n", ctx.GitBranch))
		}
	}
	if len(ctx.GitStatus) > 0 {
		sb.WriteString(fmt.Sprintf("- Changed files: %s\n", strings.Join(ctx.GitStatus, ", ")))
	}
	if ctx.ProjectType != "" && ctx.ProjectType != "unknown" {
		sb.WriteString(fmt.Sprintf("- Project: %s\n", ctx.ProjectType))
	}
	if len(ctx.DirFiles) > 0 {
		sb.WriteString(fmt.Sprintf("- Files here: %s\n", strings.Join(ctx.DirFiles, " ")))
	}

	// Last 10 recent commands from this session.
	cmds := ctx.RecentCommands
	if len(cmds) > 10 {
		cmds = cmds[len(cmds)-10:]
	}
	if len(cmds) > 0 {
		names := make([]string, len(cmds))
		for i, c := range cmds {
			names[i] = c.Command
		}
		sb.WriteString(fmt.Sprintf("- Recent: %s\n", strings.Join(names, ", ")))
	}

	// Cross-session commands the user has run before in this context.
	if len(frequent) > 0 {
		sb.WriteString(fmt.Sprintf("- Frequently used here: %s\n", strings.Join(frequent, ", ")))
	}

	// Command-transition model: what the user typically runs *after* their last
	// command, so the model can predict the natural next step.
	if len(successors) > 0 {
		sb.WriteString(fmt.Sprintf("- After your last command you usually run: %s\n", strings.Join(successors, ", ")))
	}
}
