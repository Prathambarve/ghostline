package recovery

import (
	"fmt"
	"strings"

	"github.com/prathamesh/ghostline/internal/session"
)

// SamplePrompt returns a representative recovery prompt for benchmarking, so
// latency measurements reflect a realistic payload size. MaxTokens mirrors the
// 120-token cap used by Recover.
func SamplePrompt() (prompt string, maxTokens int) {
	ctx := &session.Context{
		CWD:         "/Users/dev/project",
		GitBranch:   "main",
		GitRepo:     "ghostline",
		ProjectType: "go",
	}
	env := "Environment:\n- python resolves to /usr/bin/python\n- installed version: Python 3.13.1\n- project pins .python-version: 3.10\n"
	stderr := "SyntaxError: invalid syntax (compatibility issue across Python versions)"
	return buildPrompt("python app.py", 1, stderr, env, ctx), 120
}

func buildPrompt(cmd string, exitCode int, stderr, envContext string, ctx *session.Context) string {
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

	if envContext != "" {
		sb.WriteString(envContext)
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

	sb.WriteString("\nYou fix how a command was INVOKED or the ENVIRONMENT it ran in — not bugs in the user's code. ")
	sb.WriteString("In scope: an unknown/misspelled command (correct it to the command the user meant), ")
	sb.WriteString("wrong tool/language version or interpreter, missing module/dependency, inactive or wrong virtualenv, ")
	sb.WriteString("missing environment variable, permission problem, wrong working directory, malformed flags or arguments.\n")
	sb.WriteString("OUT OF SCOPE: bugs inside the user's own source code — syntax errors, exceptions from their logic, ")
	sb.WriteString("failing test assertions, type errors in their files. You are a shell assistant, not a code editor. ")
	sb.WriteString("If the failure is a bug in the user's source code rather than how it was run or its environment, respond with exactly: NONE\n")
	sb.WriteString("For a \"command not found\", correct it to the command the user meant (e.g. \"clde\" → \"claude\"). ")
	sb.WriteString("Do NOT suggest installing the missing program — you cannot verify a package exists, and a guessed install ")
	sb.WriteString("for a misspelling is worse than nothing. If you cannot identify the intended command, respond with exactly NONE.\n\n")

	sb.WriteString("Respond in this EXACT format (nothing else):\n")
	sb.WriteString("FIX: <the corrected command to run — runnable as-is>\n")
	sb.WriteString("WHY: <one or two short clauses naming the concrete cause>\n\n")
	sb.WriteString("FIX must be ONLY the runnable command — no explanation, no dash, nothing after it ")
	sb.WriteString("(the user runs it by pressing Enter); put all reasoning on the WHY line instead. ")
	sb.WriteString("If the command has typos, correct ALL of them, including arguments. ")
	sb.WriteString("Cite the environment facts in WHY when they explain the failure ")
	sb.WriteString("(e.g. \"python 3.13 is active but .python-version pins 3.10\"). Always include a brief WHY.\n\n")
	sb.WriteString("Example — for the failed command \"gitt stauts\":\n")
	sb.WriteString("FIX: git status\n")
	sb.WriteString("WHY: \"gitt\" and \"stauts\" were misspelled\n\n")
	sb.WriteString("Now respond for the failure above. If no specific fix is possible, respond with exactly: NONE\n")

	return sb.String()
}
