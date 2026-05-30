package recovery

import (
	"fmt"
	"strings"

	"github.com/prathamesh/ghostline/internal/session"
)

func buildPrompt(cmd string, exitCode int, stderr string, ctx *session.Context) string {
	var sb strings.Builder

	sb.WriteString("You are a terminal error recovery assistant. Be concise and specific.\n\n")
	sb.WriteString(fmt.Sprintf("Failed command: %s\n", cmd))
	sb.WriteString(fmt.Sprintf("Exit code: %d\n", exitCode))

	if stderr != "" {
		if len(stderr) > 400 {
			stderr = stderr[:400] + "..."
		}
		sb.WriteString(fmt.Sprintf("Error output:\n%s\n", stderr))
	}

	if ctx.CWD != "" {
		sb.WriteString(fmt.Sprintf("Directory: %s\n", ctx.CWD))
	}

	cmds := ctx.RecentCommands
	if len(cmds) > 3 {
		cmds = cmds[len(cmds)-3:]
	}
	if len(cmds) > 0 {
		names := make([]string, len(cmds))
		for i, c := range cmds {
			names[i] = c.Command
		}
		sb.WriteString(fmt.Sprintf("Recent commands: %s\n", strings.Join(names, ", ")))
	}

	sb.WriteString("\nRespond in this EXACT format (nothing else):\n")
	sb.WriteString("FIX: <the corrected command to run — runnable as-is>\n")
	sb.WriteString("WHY: <one short clause explaining the cause>\n\n")
	sb.WriteString("FIX must be ONLY the runnable command — no explanation, no dash, nothing after it ")
	sb.WriteString("(the user runs it by pressing Enter); put all reasoning on the WHY line instead. ")
	sb.WriteString("If the command has typos, correct ALL of them, including arguments. ")
	sb.WriteString("Always include a brief WHY (a few words).\n\n")
	sb.WriteString("Example — for the failed command \"gitt stauts\":\n")
	sb.WriteString("FIX: git status\n")
	sb.WriteString("WHY: \"gitt\" and \"stauts\" were misspelled\n\n")
	sb.WriteString("Now respond for the failure above. If no specific fix is possible, respond with exactly: NONE\n")

	return sb.String()
}
