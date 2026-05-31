package nextstep

import (
	"fmt"
	"strings"

	"github.com/prathamesh/ghostline/internal/session"
)

func buildPrompt(lastCmd string, exitCode int, ctx *session.Context) string {
	var sb strings.Builder

	sb.WriteString("You are a terminal workflow assistant. Predict the SINGLE most likely NEXT command ")
	sb.WriteString("the user will want to run — the next step in their workflow. This may be a command ")
	sb.WriteString("they have not run here yet (that's the point: anticipate the next step).\n\n")

	sb.WriteString(fmt.Sprintf("Just ran: %s\n", lastCmd))
	sb.WriteString(fmt.Sprintf("Exit code: %d\n", exitCode))

	// The recent-command trajectory is what makes this workflow-aware, not just
	// last-command-aware.
	if cmds := ctx.RecentCommands; len(cmds) > 0 {
		if len(cmds) > 5 {
			cmds = cmds[len(cmds)-5:]
		}
		names := make([]string, len(cmds))
		for i, c := range cmds {
			names[i] = c.Command
		}
		sb.WriteString(fmt.Sprintf("Recent commands: %s\n", strings.Join(names, " | ")))
	}
	if ctx.CWD != "" {
		sb.WriteString(fmt.Sprintf("Directory: %s\n", ctx.CWD))
	}
	if ctx.GitBranch != "" || ctx.GitRepo != "" {
		sb.WriteString(fmt.Sprintf("Git: %s @ %s\n", ctx.GitRepo, ctx.GitBranch))
	}
	if ctx.ProjectType != "" && ctx.ProjectType != "unknown" {
		sb.WriteString(fmt.Sprintf("Project: %s\n", ctx.ProjectType))
	}

	sb.WriteString("\nRules:\n")
	sb.WriteString("- Predict the logical NEXT step, not a repeat of what was just run.\n")
	sb.WriteString("- If the last command failed, predict the command that recovers or rolls it back.\n")
	sb.WriteString("- The command must be runnable as-is. If there is no clear next step, respond with exactly: NONE\n")
	sb.WriteString("- Mark RISK destructive if the step is irreversible or hard to undo ")
	sb.WriteString("(apply, deploy, destroy, force-push, rm, delete, drop, reset --hard); otherwise safe.\n\n")

	sb.WriteString("Respond in this EXACT format (nothing else):\n")
	sb.WriteString("NEXT: <the next command>\n")
	sb.WriteString("RISK: safe | destructive\n\n")

	sb.WriteString("Examples:\n")
	sb.WriteString("Just ran \"terraform plan\" → NEXT: terraform apply / RISK: destructive\n")
	sb.WriteString("Just ran \"git clone https://github.com/me/app.git\" → NEXT: cd app && npm install / RISK: safe\n")
	sb.WriteString("Just ran \"git add -A\" → NEXT: git commit / RISK: safe\n")
	sb.WriteString("Just ran \"docker build -t app .\" → NEXT: docker run --rm -it app / RISK: safe\n\n")

	sb.WriteString("Now respond for the command above.\n")
	return sb.String()
}

// SamplePrompt returns a representative prompt for benchmarking.
func SamplePrompt() (string, int) {
	ctx := &session.Context{CWD: "/Users/dev/app", GitRepo: "app", GitBranch: "main", ProjectType: "node"}
	return buildPrompt("terraform plan", 0, ctx), 64
}
