package completion

import (
	"fmt"
	"strings"

	"github.com/prathamesh/ghostline/internal/session"
)

func buildPrompt(buffer string, ctx *session.Context) string {
	var sb strings.Builder

	sb.WriteString("Complete this shell command. Output ONLY the continuation — the text that comes AFTER the input, not the input itself.\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Do NOT repeat the input; output only the missing suffix\n")
	sb.WriteString("- No explanation, no markdown, no code fences, no quotes\n")
	sb.WriteString("- A single line; if you cannot complete it meaningfully, output nothing\n\n")
	sb.WriteString("Example — input \"git ch\" → output \"eckout\" (so the full command becomes \"git checkout\").\n\n")

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
	if ctx.ProjectType != "" && ctx.ProjectType != "unknown" {
		sb.WriteString(fmt.Sprintf("- Project: %s\n", ctx.ProjectType))
	}

	cmds := ctx.RecentCommands
	if len(cmds) > 5 {
		cmds = cmds[len(cmds)-5:]
	}
	if len(cmds) > 0 {
		names := make([]string, len(cmds))
		for i, c := range cmds {
			names[i] = c.Command
		}
		sb.WriteString(fmt.Sprintf("- Recent: %s\n", strings.Join(names, ", ")))
	}

	sb.WriteString(fmt.Sprintf("\nInput: %s\n", buffer))
	sb.WriteString("Continuation:")

	return sb.String()
}
