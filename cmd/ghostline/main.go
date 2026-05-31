package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/prathamesh/ghostline/internal/completion"
	"github.com/prathamesh/ghostline/internal/config"
	"github.com/prathamesh/ghostline/internal/daemon"
	"github.com/prathamesh/ghostline/internal/llm"
	"github.com/prathamesh/ghostline/internal/secret"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:          "ghostline",
		Short:        "AI-powered terminal assistant",
		SilenceUsage: true,
	}

	root.AddCommand(
		serverCmd(),
		completeCmd(),
		completeMenuCmd(),
		recoverCmd(),
		contextCmd(),
		statusCmd(),
		setupCmd(),
		benchCmd(),
		backendCmd(),
		guardCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func serverCmd() *cobra.Command {
	var background bool
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the ghostline daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if !cfg.SetupComplete {
				return fmt.Errorf("run `ghostline setup` first")
			}
			if background {
				return daemon.StartBackground()
			}
			fmt.Fprintf(os.Stderr, "ghostline daemon running (%s, model: %s)\n", cfg.Backend, backendModel(cfg))
			return daemon.NewServer(cfg).Run()
		},
	}
	cmd.Flags().BoolVar(&background, "background", false, "detach and run in background")
	return cmd
}

func completeCmd() *cobra.Command {
	var buffer, sessionID string
	cmd := &cobra.Command{
		Use:   "complete",
		Short: "Get inline completion for the current command buffer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if buffer == "" {
				return nil
			}
			// Silent auto-start — never break the shell
			if err := daemon.EnsureDaemon(); err != nil {
				return nil
			}
			resp, err := daemon.SendRequest(daemon.Request{
				Type:      "complete",
				SessionID: sessionID,
				Buffer:    buffer,
				CWD:       cwd(),
			})
			if err != nil || resp == nil || resp.Type != "completion" {
				return nil
			}
			fmt.Print(resp.Suggestion)
			return nil
		},
	}
	cmd.Flags().StringVar(&buffer, "buffer", "", "current command buffer")
	cmd.Flags().StringVar(&sessionID, "session", "", "shell session ID")
	return cmd
}

// guardCmd inspects a command buffer for secret material BEFORE it runs. It is
// intentionally local-only (no daemon, no network) so it stays fast enough to
// gate every Enter and works even if the daemon is down. On a hit it prints a
// short, value-free reason to stdout and exits 2; on a clean buffer it prints
// nothing and exits 0.
func guardCmd() *cobra.Command {
	var buffer string
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Warn if a command buffer would run/commit/echo a secret",
		RunE: func(cmd *cobra.Command, args []string) error {
			if reason, found := secret.Match(buffer); found {
				fmt.Print(reason)
				os.Exit(2)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&buffer, "buffer", "", "command buffer to inspect")
	return cmd
}

// completeMenuCmd asks the daemon for several described completion candidates and
// prints them as TSV (`command\tlabel`) for the shell picker to render.
func completeMenuCmd() *cobra.Command {
	var buffer, sessionID string
	cmd := &cobra.Command{
		Use:   "complete-menu",
		Short: "List described completion candidates for the buffer (for the picker)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if buffer == "" {
				return nil
			}
			if err := daemon.EnsureDaemon(); err != nil {
				return nil
			}
			resp, err := daemon.SendRequest(daemon.Request{
				Type:      "complete_menu",
				SessionID: sessionID,
				Buffer:    buffer,
				CWD:       cwd(),
			})
			if err != nil || resp == nil {
				return nil
			}
			printCandidatesTSV(resp.Candidates)
			return nil
		},
	}
	cmd.Flags().StringVar(&buffer, "buffer", "", "current command buffer")
	cmd.Flags().StringVar(&sessionID, "session", "", "shell session ID")
	return cmd
}

// printCandidatesTSV emits one `command\tlabel` line per candidate. The picker
// displays the label but returns the command (field 1).
func printCandidatesTSV(cands []completion.Candidate) {
	var sb strings.Builder
	for _, c := range cands {
		if c.Command == "" {
			continue
		}
		sb.WriteString(c.Command)
		sb.WriteByte('\t')
		if c.Description != "" {
			sb.WriteString(c.Command + "  —  " + c.Description)
		} else {
			sb.WriteString(c.Command)
		}
		sb.WriteByte('\n')
	}
	fmt.Print(sb.String())
}

func recoverCmd() *cobra.Command {
	var cmdStr, sessionID, stderr string
	var exitCode int
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Get AI recovery suggestion for a failed command",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmdStr == "" {
				return nil
			}
			if err := daemon.EnsureDaemon(); err != nil {
				return nil
			}
			resp, err := daemon.SendRequest(daemon.Request{
				Type:      "recover",
				SessionID: sessionID,
				Command:   cmdStr,
				ExitCode:  exitCode,
				Stderr:    stderr,
			})
			if err != nil || resp == nil || resp.Type != "recovery" || resp.Fix == "" {
				return nil
			}
			// Line 1 = the runnable fix (the shell pre-fills this into the buffer).
			// Line 2 = the optional WHY (shown as an explanation, never run).
			fmt.Print(resp.Fix)
			if resp.Why != "" {
				fmt.Print("\n" + resp.Why)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cmdStr, "cmd", "", "the failed command")
	cmd.Flags().StringVar(&sessionID, "session", "", "shell session ID")
	cmd.Flags().StringVar(&stderr, "stderr", "", "stderr output from failed command")
	cmd.Flags().IntVar(&exitCode, "exit-code", 1, "exit code of failed command")
	return cmd
}

func contextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage session context",
	}

	var sessionID, cmdStr, cwdFlag string
	var exitCode int

	update := &cobra.Command{
		Use:   "update",
		Short: "Update session context after a command completes",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Fire-and-forget — if daemon isn't up, skip silently
			daemon.SendRequest(daemon.Request{ //nolint:errcheck
				Type:      "update_context",
				SessionID: sessionID,
				Command:   cmdStr,
				ExitCode:  exitCode,
				CWD:       cwdFlag,
			})
			return nil
		},
	}
	update.Flags().StringVar(&sessionID, "session", "", "shell session ID")
	update.Flags().StringVar(&cmdStr, "cmd", "", "command that ran")
	update.Flags().StringVar(&cwdFlag, "cwd", "", "working directory")
	update.Flags().IntVar(&exitCode, "exit-code", 0, "exit code")
	cmd.AddCommand(update)

	return cmd
}

// backendCmd shows or switches the inference backend without hand-editing
// config. Switching persists the choice and restarts the daemon so it takes
// effect immediately.
func backendCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "backend [anthropic|openai|groq]",
		Short:     "Show or switch the inference backend",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"anthropic", "openai", "groq"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if len(args) == 0 {
				fmt.Printf("backend: %s (model: %s)\n", cfg.Backend, backendModel(cfg))
				return nil
			}

			b := args[0]
			switch b {
			case "anthropic", "openai", "groq":
			default:
				return fmt.Errorf("unknown backend %q — choose anthropic, openai, or groq", b)
			}

			cfg.Backend = b
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			// Restart the daemon so the new backend is live immediately.
			daemon.Stop()
			time.Sleep(200 * time.Millisecond)
			if err := daemon.EnsureDaemon(); err != nil {
				fmt.Fprintf(os.Stderr, "switched to %s, but daemon restart failed: %v\n", b, err)
				return nil
			}

			fmt.Printf("✓ backend switched to %s (model: %s)\n", b, backendModel(cfg))

			// Surface readiness so the user isn't met with silent no-ops.
			if err := llm.New(cfg).Ping(context.Background()); err != nil {
				fmt.Fprintf(os.Stderr, "⚠  %s not ready: %v\n", b, err)
			}
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := daemon.SendRequest(daemon.Request{Type: "status"})
			if err != nil {
				fmt.Fprintln(os.Stderr, "ghostline: daemon not running")
				os.Exit(1)
			}
			out, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}
}

func setupCmd() *cobra.Command {
	var reconfigure bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Check prerequisites and verify ghostline is ready",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(reconfigure)
		},
	}
	cmd.Flags().BoolVar(&reconfigure, "reconfigure", false, "run the first-time setup prompts again")
	return cmd
}

func runSetup(reconfigure bool) error {
	cfg, _ := config.Load()

	if (reconfigure || !cfg.SetupComplete) && isInteractive() {
		if err := runSetupWizard(cfg); err != nil {
			return err
		}
	}

	fmt.Println("ghostline setup check")
	fmt.Println("---------------------")
	printPrivacySummary(cfg)

	ctx := context.Background()
	if err := llm.New(cfg).Ping(ctx); err != nil {
		fmt.Printf("✗  %s backend not ready: %s\n", cfg.Backend, err)
		fmt.Printf("   Set %s in your shell, or rerun: ghostline setup --reconfigure\n", backendEnvVar(cfg.Backend))
		return err
	}
	fmt.Printf("✓  %s backend ready (model: %s)\n", cfg.Backend, backendModel(cfg))
	fmt.Println("\nghostline is ready to use.")
	return nil
}

func runSetupWizard(cfg *config.Config) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("ghostline first-time setup")
	fmt.Println("--------------------------")
	fmt.Println("Ghostline does not collect telemetry or analytics.")
	fmt.Println("These choices control what local shell context may be stored and sent to your selected LLM backend.")
	fmt.Println("Press Enter to choose the Recommended option.")
	fmt.Println()

	backendChoice := promptChoice(reader, "Inference backend", []choice{
		{Key: "1", Label: "anthropic (Recommended)", Value: "anthropic"},
		{Key: "2", Label: "openai", Value: "openai"},
		{Key: "3", Label: "groq", Value: "groq"},
	}, "anthropic")
	cfg.Backend = backendChoice

	fmt.Println()
	privacyChoice := promptChoice(reader, "Privacy profile", []choice{
		{Key: "1", Label: "recommended: helpful context, redacted local history (Recommended)", Value: "recommended"},
		{Key: "2", Label: "company strict: minimal context, no history, no stderr", Value: "strict"},
		{Key: "3", Label: "custom", Value: "custom"},
	}, "recommended")

	switch privacyChoice {
	case "strict":
		applyStrictPrivacy(cfg)
	case "custom":
		applyCustomPrivacy(reader, cfg)
	default:
		applyRecommendedPrivacy(cfg)
	}

	cfg.SetupComplete = true
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	// A running daemon has already loaded its config; let the next request start
	// a fresh daemon with these choices.
	daemon.Stop() //nolint:errcheck

	fmt.Println()
	fmt.Println("Saved ~/.ghostline/config.yaml with your choices.")
	fmt.Println()
	return nil
}

type choice struct {
	Key   string
	Label string
	Value string
}

func promptChoice(reader *bufio.Reader, label string, choices []choice, fallback string) string {
	fmt.Printf("%s:\n", label)
	defaultValue := fallback
	if defaultValue == "" && len(choices) > 0 {
		defaultValue = choices[0].Value
	}
	for _, c := range choices {
		fmt.Printf("  %s) %s\n", c.Key, c.Label)
	}
	for {
		fmt.Print("> ")
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer == "" {
			return defaultValue
		}
		for _, c := range choices {
			if answer == c.Key || answer == strings.ToLower(c.Value) {
				return c.Value
			}
		}
		fmt.Println("Please choose one of the listed options.")
	}
}

func applyRecommendedPrivacy(cfg *config.Config) {
	cfg.HistoryEnabled = true
	cfg.SendCWD = true
	cfg.SendGitRemote = true
	cfg.SendGitStatus = true
	cfg.SendDirFiles = true
	cfg.SendRecentCommands = true
	cfg.SendStderr = true
}

func applyStrictPrivacy(cfg *config.Config) {
	cfg.HistoryEnabled = false
	cfg.SendCWD = false
	cfg.SendGitRemote = false
	cfg.SendGitStatus = false
	cfg.SendDirFiles = false
	cfg.SendRecentCommands = false
	cfg.SendStderr = false
}

func applyCustomPrivacy(reader *bufio.Reader, cfg *config.Config) {
	fmt.Println()
	fmt.Println("Custom privacy choices. Enter keeps the Recommended value.")
	cfg.HistoryEnabled = promptYesNo(reader, "Keep redacted local command history for cross-session suggestions? (Recommended: yes)", true)
	cfg.SendCWD = promptYesNo(reader, "Send current directory/project context to the model? (Recommended: yes)", true)
	cfg.SendGitRemote = promptYesNo(reader, "Send git remote repo name when available? (Recommended: yes)", true)
	cfg.SendGitStatus = promptYesNo(reader, "Send changed file names from git status? (Recommended: yes)", true)
	cfg.SendDirFiles = promptYesNo(reader, "Send nearby file names for better completions? (Recommended: yes)", true)
	cfg.SendRecentCommands = promptYesNo(reader, "Send recent commands from this shell session? (Recommended: yes)", true)
	cfg.SendStderr = promptYesNo(reader, "Send stderr from failed commands for recovery? (Recommended: yes)", true)
}

func promptYesNo(reader *bufio.Reader, question string, recommended bool) bool {
	suffix := "Y/n"
	if !recommended {
		suffix = "y/N"
	}
	for {
		fmt.Printf("%s [%s] ", question, suffix)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		switch answer {
		case "":
			return recommended
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			fmt.Println("Please answer yes or no.")
		}
	}
}

func printPrivacySummary(cfg *config.Config) {
	fmt.Printf("backend: %s (model: %s)\n", cfg.Backend, backendModel(cfg))
	fmt.Printf("history: %s\n", enabledText(cfg.HistoryEnabled))
	fmt.Printf("context sent: cwd=%s git_remote=%s git_status=%s dir_files=%s recent_commands=%s stderr=%s\n",
		enabledText(cfg.SendCWD),
		enabledText(cfg.SendGitRemote),
		enabledText(cfg.SendGitStatus),
		enabledText(cfg.SendDirFiles),
		enabledText(cfg.SendRecentCommands),
		enabledText(cfg.SendStderr),
	)
}

func enabledText(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func isInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func backendEnvVar(backend string) string {
	switch backend {
	case "openai":
		return "OPENAI_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	default:
		return "ANTHROPIC_API_KEY"
	}
}

func cwd() string {
	d, _ := os.Getwd()
	return d
}
