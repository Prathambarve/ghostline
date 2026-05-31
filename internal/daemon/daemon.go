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
	"github.com/prathamesh/ghostline/internal/nextstep"
	"github.com/prathamesh/ghostline/internal/recovery"
	"github.com/prathamesh/ghostline/internal/session"
)

// maxHistoryLines bounds the persisted command log.
const maxHistoryLines = 5000

// maxFixCacheLines bounds the persisted error→fix recovery cache.
const maxFixCacheLines = 2000

// maxPredictCacheLines bounds the persisted next-step prediction cache.
const maxPredictCacheLines = 2000

// offerTTL bounds how long a pending recovery offer waits to be accepted. The
// shell pre-fills the fix into the very next prompt, so acceptance is normally
// immediate; this just keeps a stale, ignored offer from being learned much
// later if the user happens to type the same command.
const offerTTL = 5 * time.Minute

// correctionTTL bounds how long a failed command waits for the user's own
// correction. Self-correction looks only at the immediately-following command, so
// this just discards a stale failure if the user walks away mid-fix.
const correctionTTL = 2 * time.Minute

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
	// EnvContext is a pre-rendered block of environment facts (tool version/path,
	// project version pins, active venv) gathered client-side by `ghostline
	// recover`, which runs in the shell and so sees the real PATH/$VIRTUAL_ENV.
	EnvContext string `json:"env_context"`
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
	// Prediction (next-step) response.
	Prediction  string `json:"prediction,omitempty"`
	Destructive bool   `json:"destructive,omitempty"`
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

// failure is the last command that failed in a session, awaiting the user's own
// correction. If their next command is a close, successful match we learn it as a
// fix even though Ghostline never offered it (self-correction learning).
type failure struct {
	cmd    string
	stderr string
	repo   string
	at     time.Time
}

type Server struct {
	cfg         *config.Config
	store       *session.Store
	history     *history.Store
	fixcache    *fixcache.Store
	completer   *completion.Completer
	recoverer   *recovery.Recovery
	predictor   *nextstep.Predictor
	predictache *nextstep.Store

	offersMu sync.Mutex
	offers   map[string]offer // sessionID → last LLM fix awaiting acceptance

	lastFailMu sync.Mutex
	lastFail   map[string]failure // sessionID → last failed command awaiting a self-correction
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
	var pcache *nextstep.Store
	if cfg.PredictEnabled {
		if path, err := config.PredictCachePath(); err == nil {
			pcache = nextstep.NewStore(path, maxPredictCacheLines)
		}
	}
	return &Server{
		cfg:         cfg,
		store:       session.NewStore(cfg.MaxContextCommands),
		history:     hist,
		fixcache:    fcache,
		completer:   completion.New(gen, cfg.CompletionTimeoutMS),
		recoverer:   recovery.New(gen, cfg.RecoveryTimeoutMS),
		predictor:   nextstep.New(gen, cfg.RecoveryTimeoutMS),
		predictache: pcache,
		offers:      make(map[string]offer),
		lastFail:    make(map[string]failure),
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
	case "predict":
		resp = s.handlePredict(req)
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
	envContext := req.EnvContext
	if !s.cfg.SendEnvProbe {
		envContext = "" // defense in depth; the client should not have sent it
	}
	if !s.cfg.SendRecentCommands {
		ctx.RecentCommands = nil
	}

	// Remember this failure so that if the user's very next command is a close
	// correction we can learn it even though we never offered it (self-correction
	// learning). Recorded here, not in handleUpdateContext, because this is where
	// the real stderr lives — so the learned key matches a future lookup.
	if s.fixcache != nil {
		s.lastFailMu.Lock()
		s.lastFail[req.SessionID] = failure{
			cmd:    strings.TrimSpace(req.Command),
			stderr: stderr,
			repo:   ctx.GitRepo,
			at:     time.Now(),
		}
		s.lastFailMu.Unlock()
	}

	// Tier 0: replay a fix the user previously accepted for this exact failure —
	// instant, offline, no API round-trip. This is the payoff of learning.
	if s.fixcache != nil {
		if e := s.fixcache.Lookup(req.Command, stderr, ctx.GitRepo); e != nil {
			return recoveryResponse(e.Fix, e.Why)
		}
	}

	result, err := s.recoverer.Recover(req.Command, req.ExitCode, stderr, envContext, &ctx)
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

// handlePredict returns the predicted next workflow step for the just-run
// command. Gated to workflow moments; replays a cached prediction instantly
// (offline) and only calls the model on a miss, then caches it.
func (s *Server) handlePredict(req Request) Response {
	if !s.cfg.PredictEnabled {
		return Response{Type: "none"}
	}
	lastCmd := strings.TrimSpace(req.Command)
	if !nextstep.ShouldPredict(lastCmd, req.ExitCode) {
		return Response{Type: "none"}
	}

	ctx := *s.store.Get(req.SessionID)
	if s.cfg.SendCWD && req.CWD != "" && ctx.CWD == "" {
		ctx.CWD = req.CWD
	}
	if !s.cfg.SendRecentCommands {
		ctx.RecentCommands = nil
	}

	// Tier 0: replay a cached prediction for this exact (cmd, repo) — instant,
	// offline. This is the workflow flywheel: the model's leap, learned once.
	if s.predictache != nil {
		if e := s.predictache.Lookup(lastCmd, ctx.GitRepo); e != nil {
			return Response{Type: "prediction", Prediction: e.Next, Destructive: e.Destructive}
		}
	}

	res, err := s.predictor.Predict(lastCmd, req.ExitCode, &ctx)
	if err != nil || res == nil {
		return Response{Type: "none"}
	}

	if s.predictache != nil {
		s.predictache.Learn(lastCmd, ctx.GitRepo, res.Next, res.Destructive) //nolint:errcheck
	}
	return Response{Type: "prediction", Prediction: res.Next, Destructive: res.Destructive}
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

// learnSelfCorrection records a fix the user discovered on their own: when a
// command succeeds right after a close-but-failed command, that success is the
// correction, so the next identical failure replays it instantly and offline — no
// model call, no prior offer. Only the command immediately following the failure
// is considered (any other success consumes the pending failure), which keeps
// unrelated back-to-back commands from being mistaken for a fix.
func (s *Server) learnSelfCorrection(sessionID, command string, exitCode int) {
	if s.fixcache == nil || exitCode != 0 {
		return // failures are recorded in handleRecover, where the real stderr lives
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}

	s.lastFailMu.Lock()
	f, ok := s.lastFail[sessionID]
	if ok {
		delete(s.lastFail, sessionID) // consume: only the immediately-next success counts
	}
	s.lastFailMu.Unlock()

	if !ok || time.Since(f.at) > correctionTTL || !looksLikeCorrection(f.cmd, command) {
		return
	}
	s.fixcache.Learn(f.cmd, f.stderr, f.repo, command, "learned from your earlier correction") //nolint:errcheck
}

// looksLikeCorrection reports whether fixed is plausibly a typo-correction of
// failed. The whole command must be a close edit AND the command name (first
// token) must match or itself be a close typo — the latter stops a shared
// argument from making two different commands look like a correction (e.g.
// "cat a.txt" vs "rm a.txt"). The length floor and per-three-characters edit
// budget keep unrelated short commands ("ls" then "cd") and same-verb-different-
// subcommand pairs ("git status" then "git push") from being learned.
func looksLikeCorrection(failed, fixed string) bool {
	failed, fixed = strings.TrimSpace(failed), strings.TrimSpace(fixed)
	if failed == "" || fixed == "" || failed == fixed {
		return false
	}
	if !closeEdit(failed, fixed) {
		return false
	}
	tf, tx := firstField(failed), firstField(fixed)
	return tf == tx || closeEdit(tf, tx)
}

// closeEdit reports whether a and b are within ~one edit per three characters,
// with a length floor so two short, different tokens don't qualify.
func closeEdit(a, b string) bool {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen < 3 {
		return false
	}
	d := osaDistance(a, b)
	return d >= 1 && d*3 <= maxLen+2
}

func firstField(s string) string {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

// osaDistance is the optimal string alignment distance (Levenshtein plus adjacent
// transpositions, which are the most common typo), computed over bytes with three
// rolling rows.
func osaDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev2 := make([]int, lb+1)
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m := prev[j] + 1 // deletion
			if ins := curr[j-1] + 1; ins < m {
				m = ins
			}
			if sub := prev[j-1] + cost; sub < m {
				m = sub
			}
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if t := prev2[j-2] + 1; t < m {
					m = t
				}
			}
			curr[j] = m
		}
		prev2, prev, curr = prev, curr, prev2
	}
	return prev[lb]
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

	// If this successful command is a close correction of the command that just
	// failed, learn it on the user's behalf — the offline flywheel.
	s.learnSelfCorrection(req.SessionID, req.Command, req.ExitCode)

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
