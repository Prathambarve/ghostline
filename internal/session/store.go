package session

import (
	"sync"
	"time"
)

type CmdRecord struct {
	Command   string    `json:"cmd"`
	ExitCode  int       `json:"exit_code"`
	Stderr    string    `json:"stderr"`
	CWD       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
}

type Context struct {
	SessionID      string      `json:"session_id"`
	CWD            string      `json:"cwd"`
	GitBranch      string      `json:"git_branch"`
	GitRepo        string      `json:"git_repo"`
	ProjectType    string      `json:"project_type"`
	RecentCommands []CmdRecord `json:"recent_commands"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type Update struct {
	SessionID   string
	CWD         string
	Command     string
	ExitCode    int
	Stderr      string
	GitBranch   string
	GitRepo     string
	ProjectType string
}

type Store struct {
	mu      sync.RWMutex
	data    map[string]*Context
	maxCmds int
}

func NewStore(maxCmds int) *Store {
	s := &Store{
		data:    make(map[string]*Context),
		maxCmds: maxCmds,
	}
	go s.evictLoop()
	return s
}

func (s *Store) Get(id string) *Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ctx, ok := s.data[id]; ok {
		return ctx
	}
	return &Context{SessionID: id}
}

func (s *Store) Apply(u Update) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, ok := s.data[u.SessionID]
	if !ok {
		ctx = &Context{SessionID: u.SessionID}
		s.data[u.SessionID] = ctx
	}

	if u.CWD != "" {
		ctx.CWD = u.CWD
	}
	if u.GitBranch != "" {
		ctx.GitBranch = u.GitBranch
	}
	if u.GitRepo != "" {
		ctx.GitRepo = u.GitRepo
	}
	if u.ProjectType != "" {
		ctx.ProjectType = u.ProjectType
	}
	if u.Command != "" {
		rec := CmdRecord{
			Command:   u.Command,
			ExitCode:  u.ExitCode,
			Stderr:    u.Stderr,
			CWD:       u.CWD,
			Timestamp: time.Now(),
		}
		ctx.RecentCommands = append(ctx.RecentCommands, rec)
		if len(ctx.RecentCommands) > s.maxCmds {
			ctx.RecentCommands = ctx.RecentCommands[len(ctx.RecentCommands)-s.maxCmds:]
		}
	}
	ctx.UpdatedAt = time.Now()
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

func (s *Store) evictLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-24 * time.Hour)
		s.mu.Lock()
		for id, ctx := range s.data {
			if ctx.UpdatedAt.Before(cutoff) {
				delete(s.data, id)
			}
		}
		s.mu.Unlock()
	}
}
