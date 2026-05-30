package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/prathamesh/ghostline/internal/completion"
	"github.com/prathamesh/ghostline/internal/config"
	ctxdetect "github.com/prathamesh/ghostline/internal/context"
	"github.com/prathamesh/ghostline/internal/llm"
	"github.com/prathamesh/ghostline/internal/recovery"
	"github.com/prathamesh/ghostline/internal/session"
)

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
	Suggestion string `json:"suggestion,omitempty"`
	Fix        string `json:"fix,omitempty"`
	Why        string `json:"why,omitempty"`
	Type       string `json:"type"`
	Status     string `json:"status,omitempty"`
	Model      string `json:"model,omitempty"`
	Sessions   int    `json:"sessions,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Server struct {
	cfg       *config.Config
	store     *session.Store
	completer *completion.Completer
	recoverer *recovery.Recovery
}

func NewServer(cfg *config.Config) *Server {
	gen := llm.New(cfg)
	return &Server{
		cfg:       cfg,
		store:     session.NewStore(cfg.MaxContextCommands),
		completer: completion.New(gen, cfg.CompletionTimeoutMS),
		recoverer: recovery.New(gen, cfg.RecoveryTimeoutMS),
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
	if s.cfg.Backend == "ollama" {
		return s.cfg.Model
	}
	return s.cfg.AnthropicModel
}

func (s *Server) handleComplete(req Request) Response {
	ctx := s.store.Get(req.SessionID)
	if req.CWD != "" && ctx.CWD == "" {
		ctx.CWD = req.CWD
	}

	suggestion, err := s.completer.Complete(req.Buffer, ctx)
	if err != nil {
		return Response{Type: "none"}
	}
	if suggestion == "" {
		return Response{Type: "none"}
	}
	return Response{Type: "completion", Suggestion: suggestion}
}

func (s *Server) handleRecover(req Request) Response {
	ctx := s.store.Get(req.SessionID)

	result, err := s.recoverer.Recover(req.Command, req.ExitCode, req.Stderr, ctx)
	if err != nil || result == nil {
		return Response{Type: "none"}
	}

	display := result.Fix
	if result.Why != "" {
		display = result.Fix + " — " + result.Why
	}
	return Response{Type: "recovery", Fix: result.Fix, Why: result.Why, Suggestion: display}
}

func (s *Server) handleUpdateContext(req Request) Response {
	var gitBranch, gitRepo, projectType string
	if req.CWD != "" {
		git := ctxdetect.DetectGit(req.CWD)
		gitBranch = git.Branch
		gitRepo = git.Repo
		projectType = ctxdetect.DetectProject(req.CWD)
	}

	s.store.Apply(session.Update{
		SessionID:   req.SessionID,
		CWD:         req.CWD,
		Command:     req.Command,
		ExitCode:    req.ExitCode,
		Stderr:      req.Stderr,
		GitBranch:   gitBranch,
		GitRepo:     gitRepo,
		ProjectType: projectType,
	})

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
