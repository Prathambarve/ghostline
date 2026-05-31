package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prathamesh/ghostline/internal/completion"
	"github.com/prathamesh/ghostline/internal/config"
	ctxdetect "github.com/prathamesh/ghostline/internal/context"
	"github.com/prathamesh/ghostline/internal/fixcache"
	"github.com/prathamesh/ghostline/internal/history"
	"github.com/prathamesh/ghostline/internal/llm"
	"github.com/prathamesh/ghostline/internal/recovery"
	"github.com/prathamesh/ghostline/internal/session"
)

// maxHistoryLines bounds the persisted command log.
const maxHistoryLines = 5000

// maxFixCacheLines bounds the persisted error→fix recovery cache.
const maxFixCacheLines = 2000

// offerTTL bounds how long a pending recovery offer waits to be accepted. The
// shell pre-fills the fix into the very next prompt, so acceptance is normally
// immediate; this just keeps a stale, ignored offer from being learned much
// later if the user happens to type the same command.
const offerTTL = 5 * time.Minute

// frequentSuggestions is how many cross-session commands we surface to the model.
const frequentSuggestions = 10

// transitionSuggestions is how many predicted next-commands (from the command-
// transition model) we surface to the model.
const transitionSuggestions = 5

type Request struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Buffer    string `json:"buffer"`
	CWD       string `json:"cwd"`
	Command   string `json:"cmd"`
	ExitCode  int    `json:"exit_code"`
	Stderr    string `json:"stderr"`
}

type Response struct {
	Suggestion string                 `json:"suggestion,omitempty"`
	Fix        string                 `json:"fix,omitempty"`
	Why        string                 `json:"why,omitempty"`
	Type       string                 `json:"type"`
	Status     string                 `json:"status,omitempty"`
	Model      string                 `json:"model,omitempty"`
	Sessions   int                    `json:"sessions,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Candidates []completion.Candidate `json:"candidates,omitempty"`
}

// offer is a recovery suggestion awaiting the user's verdict. We hold the key
// the fix was produced for so that when the user runs the fix and it succeeds,
// we can learn (key → fix) into the cache.
type offer struct {
	cmd    string
	stderr string
	repo   string
	fix    string
	why    string
	at     time.Time
}

type Server struct {
	cfg       *config.Config
	store     *session.Store
	history   *history.Store
	fixcache  *fixcache.Store
	completer *completion.Completer
	recoverer *recovery.Recovery

	offersMu sync.Mutex
	offers   map[string]offer // sessionID → last LLM fix awaiting acceptance
}

func NewServer(cfg *config.Config) *Server {
	gen := llm.New(cfg)
	var hist *history.Store
	var fcache *fixcache.Store
	if cfg.HistoryEnabled {
		if path, err := config.HistoryPath(); err == nil {
			hist = history.NewStore(path, maxHistoryLines)
		}
		if path, err := config.FixCachePath(); err == nil {
			fcache = fixcache.NewStore(path, maxFixCacheLines)
		}
	}
	return &Server{
		cfg:       cfg,
		store:     session.NewStore(cfg.MaxContextCommands),
		history:   hist,
		fixcache:  fcache,
		completer: completion.New(gen, cfg.CompletionTimeoutMS),
		recoverer: recovery.New(gen, cfg.RecoveryTimeoutMS),
		offers:    make(map[string]offer),
	}
}

func (s *Server) Run() error {
	sockPath, err := config.SocketPath()
	if err != nil {
		return err
	}

	dir, _ := config.Dir()
	os.MkdirAll(dir, 0700)

	if pidPath, _ := config.PIDPath(); pidPath != "" {
		os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0600)
	}

	// Remove stale socket
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sockPath, err)
	}
	defer func() {
		ln.Close()
		os.Remove(sockPath)
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeResp(conn, Response{Type: "error", Error: "invalid json"})
		return
	}

	var resp Response
	switch req.Type {
	case "complete":
		resp = s.handleComplete(req)
	case "complete_menu":
		resp = s.handleCompleteMenu(req)
	case "recover":
		resp = s.handleRecover(req)
	case "update_context":
		resp = s.handleUpdateContext(req)
	case "status":
		resp = Response{
			Type:     "status",
			Status:   "ok",
			Model:    s.activeModel(),
			Sessions: s.store.Count(),
		}
	default:
		resp = Response{Type: "error", Error: "unknown type: " + req.Type}
	}

	writeResp(conn, resp)
}

// activeModel reports the model backing the currently selected backend.
func (s *Server) activeModel() string {
	switch s.cfg.Backend {
	case "openai":
		return s.cfg.OpenAIModel
	case "groq":
		return s.cfg.GroqModel
	default:
		return s.cfg.AnthropicModel
	}
}

// completionContext assembles the session context plus cross-session signals
// (frequently-used and likely-next commands) shared by the single-suggestion and
// menu completion paths.
func (s *Server) completionContext(req Request) (session.Context, []string, []string) {
	ctx := *s.store.Get(req.SessionID)
	if s.cfg.SendCWD && req.CWD != "" && ctx.CWD == "" {
		ctx.CWD = req.CWD
	}
	if !s.cfg.SendRecentCommands {
		ctx.RecentCommands = nil
	}

	var frequent, successors []string
	if s.history != nil {
		frequent = s.history.Frequent(ctx.GitRepo, ctx.CWD, ctx.ProjectType, frequentSuggestions)
		if n := len(ctx.RecentCommands); n > 0 {
			prev := ctx.RecentCommands[n-1].Command
			successors = s.history.Successors(prev, ctx.GitRepo, ctx.CWD, transitionSuggestions)
		}
	}
	return ctx, frequent, successors
}

func (s *Server) handleComplete(req Request) Response {
	ctx, frequent, successors := s.completionContext(req)

	suggestion, err := s.completer.Complete(req.Buffer, &ctx, frequent, successors)
	if err != nil || suggestion == "" {
		return Response{Type: "none"}
	}
	return Response{Type: "completion", Suggestion: suggestion}
}

// handleCompleteMenu returns several described candidate completions — the data
// behind the Fig-style picker.
func (s *Server) handleCompleteMenu(req Request) Response {
	ctx, frequent, successors := s.completionContext(req)

	cands, err := s.completer.CompleteMenu(req.Buffer, &ctx, frequent, successors)
	if err != nil || len(cands) == 0 {
		return Response{Type: "none"}
	}
	return Response{Type: "menu", Candidates: cands}
}

func (s *Server) handleRecover(req Request) Response {
	ctx := *s.store.Get(req.SessionID)
	stderr := req.Stderr
	if !s.cfg.SendStderr {
		stderr = ""
	}
	if !s.cfg.SendRecentCommands {
		ctx.RecentCommands = nil
	}

	// Tier 0: replay a fix the user previously accepted for this exact failure —
	// instant, offline, no API round-trip. This is the payoff of learning.
	if s.fixcache != nil {
		if e := s.fixcache.Lookup(req.Command, stderr, ctx.GitRepo); e != nil {
			return recoveryResponse(e.Fix, e.Why)
		}
	}

	result, err := s.recoverer.Recover(req.Command, req.ExitCode, stderr, &ctx)
	if err != nil || result == nil {
		return Response{Type: "none"}
	}

	// Remember the offer so we can learn it if the user accepts it (runs the fix
	// and it succeeds — detected in handleUpdateContext). Only LLM-tier fixes are
	// worth caching; deterministic typo fixes are already instant and offline.
	if s.fixcache != nil && result.Source == "llm" {
		s.offersMu.Lock()
		s.offers[req.SessionID] = offer{
			cmd:    req.Command,
			stderr: stderr,
			repo:   ctx.GitRepo,
			fix:    result.Fix,
			why:    result.Why,
			at:     time.Now(),
		}
		s.offersMu.Unlock()
	}

	return recoveryResponse(result.Fix, result.Why)
}

// recoveryResponse builds the wire response for a fix, including the combined
// display string the shell prints.
func recoveryResponse(fix, why string) Response {
	display := fix
	if why != "" {
		display = fix + " — " + why
	}
	return Response{Type: "recovery", Fix: fix, Why: why, Suggestion: display}
}

// learnIfAccepted checks whether the just-run command was the fix we offered
// this session and, if it succeeded, records it in the recovery cache. A
// non-matching command leaves the offer in place: the failed command's own
// async context-update can race ahead of the user's acceptance, and we must not
// drop the offer before the fix actually runs. The offer is consumed once the
// fix runs (pass or fail), overwritten by the next recovery, or expired by TTL.
func (s *Server) learnIfAccepted(sessionID, command string, exitCode int) {
	if s.fixcache == nil {
		return
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}

	s.offersMu.Lock()
	o, ok := s.offers[sessionID]
	if !ok {
		s.offersMu.Unlock()
		return
	}
	if command != strings.TrimSpace(o.fix) {
		// Not the fix (could be the original failure's own update, or an
		// unrelated command). Leave the offer pending unless it has gone stale.
		if time.Since(o.at) > offerTTL {
			delete(s.offers, sessionID)
		}
		s.offersMu.Unlock()
		return
	}
	// The fix ran: resolve the offer regardless of outcome.
	delete(s.offers, sessionID)
	s.offersMu.Unlock()

	if exitCode != 0 || time.Since(o.at) > offerTTL {
		return // ran but failed, or accepted too late — don't cache a bad fix
	}
	s.fixcache.Learn(o.cmd, o.stderr, o.repo, o.fix, o.why) //nolint:errcheck
}

func (s *Server) handleUpdateContext(req Request) Response {
	var gitBranch, gitRepo, projectType string
	var gitStatus, dirFiles []string
	if req.CWD != "" {
		git := ctxdetect.DetectGit(req.CWD)
		gitBranch = git.Branch
		projectType = ctxdetect.DetectProject(req.CWD)
		if s.cfg.SendGitRemote {
			gitRepo = git.Repo
		}
		if s.cfg.SendGitStatus {
			gitStatus = ctxdetect.DetectGitStatus(req.CWD)
		}
		if s.cfg.SendDirFiles {
			dirFiles = ctxdetect.DetectDirFiles(req.CWD)
		}
	}

	cwd := req.CWD
	if !s.cfg.SendCWD {
		cwd = ""
	}
	command := req.Command
	stderr := req.Stderr
	if !s.cfg.SendRecentCommands {
		command = ""
	}
	if !s.cfg.SendStderr {
		stderr = ""
	}

	s.store.Apply(session.Update{
		SessionID:   req.SessionID,
		CWD:         cwd,
		Command:     command,
		ExitCode:    req.ExitCode,
		Stderr:      stderr,
		GitBranch:   gitBranch,
		GitRepo:     gitRepo,
		GitStatus:   gitStatus,
		ProjectType: projectType,
		DirFiles:    dirFiles,
	})

	// If this command was the recovery fix we offered, and it worked, learn it
	// so an identical future failure replays instantly. Uses the raw command
	// (not the privacy-gated one) since acceptance is matched against the fix we
	// actually suggested.
	s.learnIfAccepted(req.SessionID, req.Command, req.ExitCode)

	// Persist a redacted record for cross-session recall (best-effort; secrets
	// are dropped/masked inside history.Append).
	if s.history != nil && req.Command != "" {
		s.history.Append(history.Record{ //nolint:errcheck
			Command:     req.Command,
			ExitCode:    req.ExitCode,
			CWD:         cwd,
			GitRepo:     gitRepo,
			ProjectType: projectType,
		})
	}

	return Response{Type: "ok"}
}

func writeResp(conn net.Conn, resp Response) {
	data, _ := json.Marshal(resp)
	conn.Write(append(data, '\n'))
}

// SendRequest sends a single request to the running daemon and returns the response.
func SendRequest(req Request) (*Response, error) {
	sockPath, err := config.SocketPath()
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("daemon not running")
	}
	defer conn.Close()

	data, _ := json.Marshal(req)
	conn.Write(append(data, '\n'))

	var resp Response
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		json.Unmarshal(scanner.Bytes(), &resp)
	}
	return &resp, nil
}

// EnsureDaemon starts the daemon in the background if not already running.
func EnsureDaemon() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.SetupComplete {
		return fmt.Errorf("run `ghostline setup` first")
	}

	sockPath, err := config.SocketPath()
	if err != nil {
		return err
	}

	if conn, err := net.Dial("unix", sockPath); err == nil {
		conn.Close()
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "server")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait up to 3s for socket to appear
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if conn, err := net.Dial("unix", sockPath); err == nil {
			conn.Close()
			return nil
		}
	}

	return fmt.Errorf("daemon did not start within 3s")
}

// Stop terminates a running daemon (via its PID file) and removes the socket so
// the next call starts a fresh one. No-op if nothing is running.
func Stop() error {
	pidPath, err := config.PIDPath()
	if err != nil {
		return err
	}
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				proc.Signal(syscall.SIGTERM)
			}
		}
	}
	if sock, _ := config.SocketPath(); sock != "" {
		os.Remove(sock)
	}
	return nil
}

// StartBackground re-execs self as a detached background daemon.
func StartBackground() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "server")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	fmt.Fprintf(os.Stderr, "ghostline daemon started (pid %d)\n", cmd.Process.Pid)
	return nil
}
