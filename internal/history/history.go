// Package history persists a redacted, append-only log of shell commands so
// Ghostline can make cross-session suggestions — e.g. a login command typed in
// one tab is recalled in a new tab. Only command text, exit code, cwd, repo and
// project type are stored; never stderr or command output.
package history

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type Record struct {
	Command     string    `json:"cmd"`
	ExitCode    int       `json:"exit_code"`
	CWD         string    `json:"cwd"`
	GitRepo     string    `json:"git_repo"`
	ProjectType string    `json:"project_type"`
	Timestamp   time.Time `json:"ts"`
}

type Store struct {
	mu       sync.Mutex
	path     string
	maxLines int
}

// NewStore opens (and compacts) the history file at path, keeping at most
// maxLines most-recent entries.
func NewStore(path string, maxLines int) *Store {
	s := &Store{path: path, maxLines: maxLines}
	s.compact()
	return s
}

// Append redacts and writes one command record. Commands matching the secret
// denylist are dropped entirely (never touch disk). Empty/trivial commands and
// missing paths are skipped. Returns nil on a deliberate skip — callers treat
// history as best-effort.
func (s *Store) Append(r Record) error {
	cmd := strings.TrimSpace(r.Command)
	if cmd == "" {
		return nil
	}
	cleaned, ok := clean(cmd)
	if !ok {
		return nil
	}
	r.Command = cleaned
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// Load reads all records from disk, oldest first. Malformed lines are skipped.
func (s *Store) Load() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() []Record {
	f, err := os.Open(s.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var recs []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err == nil && r.Command != "" {
			recs = append(recs, r)
		}
	}
	return recs
}

// Frequent returns up to limit distinct successful commands relevant to the
// given context (same repo, or same directory), ranked by frequency with a
// recency tiebreak. This is what surfaces a user's repeated rituals in a brand
// new shell session.
func (s *Store) Frequent(repo, cwd, projectType string, limit int) []string {
	recs := s.Load()
	if len(recs) == 0 {
		return nil
	}

	type agg struct {
		count int
		last  time.Time
		order int // first-seen index, for stable ordering
	}
	stats := make(map[string]*agg)
	for i, r := range recs {
		if r.ExitCode != 0 {
			continue // only recall commands that worked
		}
		if !relevant(r, repo, cwd) {
			continue
		}
		a, ok := stats[r.Command]
		if !ok {
			a = &agg{order: i}
			stats[r.Command] = a
		}
		a.count++
		if r.Timestamp.After(a.last) {
			a.last = r.Timestamp
		}
	}

	cmds := make([]string, 0, len(stats))
	for c := range stats {
		cmds = append(cmds, c)
	}
	sort.Slice(cmds, func(i, j int) bool {
		ai, aj := stats[cmds[i]], stats[cmds[j]]
		if ai.count != aj.count {
			return ai.count > aj.count
		}
		if !ai.last.Equal(aj.last) {
			return ai.last.After(aj.last)
		}
		return ai.order < aj.order
	})

	if len(cmds) > limit {
		cmds = cmds[:limit]
	}
	return cmds
}

// transitionWindow bounds how far apart two logged commands can be and still be
// treated as a sequence. The history log interleaves all shells (no session id),
// so file-adjacency alone could stitch together commands from different sessions;
// requiring them to be close in time keeps a "transition" meaningful.
const transitionWindow = 10 * time.Minute

// Successors returns the commands that have historically followed prev in the
// current working context, ranked by how often they did (recency breaks ties).
// This is the command-transition model: given the user's last command, it
// predicts likely next commands — "after `git add -A` you usually run
// `git commit`". It only suggests commands that succeeded, never repeats prev,
// and approximates per-session sequences via file adjacency bounded by
// transitionWindow (see above). Returns nil when prev is empty or unseen.
func (s *Store) Successors(prev, repo, cwd string, limit int) []string {
	prev = strings.TrimSpace(prev)
	if prev == "" {
		return nil
	}
	recs := s.Load()
	if len(recs) < 2 {
		return nil
	}

	type agg struct {
		count int
		last  time.Time
		order int // first-seen index, for stable ordering
	}
	stats := make(map[string]*agg)
	for i := 0; i+1 < len(recs); i++ {
		a, b := recs[i], recs[i+1]
		if a.Command != prev {
			continue
		}
		if b.ExitCode != 0 {
			continue // only suggest a next command that worked
		}
		if b.Command == prev {
			continue // a repeat isn't a useful "next step"
		}
		if !relevant(a, repo, cwd) || !relevant(b, repo, cwd) {
			continue // both ends must belong to the current context
		}
		if !a.Timestamp.IsZero() && !b.Timestamp.IsZero() {
			if gap := b.Timestamp.Sub(a.Timestamp); gap < 0 || gap > transitionWindow {
				continue // too far apart — likely a different session
			}
		}
		ag, ok := stats[b.Command]
		if !ok {
			ag = &agg{order: i}
			stats[b.Command] = ag
		}
		ag.count++
		if b.Timestamp.After(ag.last) {
			ag.last = b.Timestamp
		}
	}

	cmds := make([]string, 0, len(stats))
	for c := range stats {
		cmds = append(cmds, c)
	}
	sort.Slice(cmds, func(i, j int) bool {
		ai, aj := stats[cmds[i]], stats[cmds[j]]
		if ai.count != aj.count {
			return ai.count > aj.count
		}
		if !ai.last.Equal(aj.last) {
			return ai.last.After(aj.last)
		}
		return ai.order < aj.order
	})

	if len(cmds) > limit {
		cmds = cmds[:limit]
	}
	return cmds
}

// relevant decides whether a record belongs to the current working context.
// Prefer a repo match; fall back to an exact directory match.
func relevant(r Record, repo, cwd string) bool {
	if repo != "" && r.GitRepo == repo {
		return true
	}
	if cwd != "" && r.CWD == cwd {
		return true
	}
	return false
}

// compact trims the file to the last maxLines entries on startup and, in the
// same pass, retroactively scrubs records under the *current* secret policy —
// so a credential stored under older, weaker rules (e.g. a provider key prefix
// the denylist now recognizes) is dropped or re-redacted the next time the
// daemon starts. Rewrites only when something actually changed, to avoid
// needless disk churn.
func (s *Store) compact() {
	s.mu.Lock()
	defer s.mu.Unlock()

	recs := s.loadLocked()
	if len(recs) == 0 {
		return
	}

	scrubbed := make([]Record, 0, len(recs))
	changed := false
	for _, r := range recs {
		cleaned, ok := clean(r.Command)
		if !ok {
			changed = true // a now-denylisted record is being dropped
			continue
		}
		if cleaned != r.Command {
			changed = true // re-redacted under tighter rules
			r.Command = cleaned
		}
		scrubbed = append(scrubbed, r)
	}

	if s.maxLines > 0 && len(scrubbed) > s.maxLines {
		scrubbed = scrubbed[len(scrubbed)-s.maxLines:]
		changed = true
	}

	if !changed {
		return
	}

	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for _, r := range scrubbed {
		line, err := json.Marshal(r)
		if err != nil {
			continue
		}
		w.Write(append(line, '\n'))
	}
	w.Flush()
	f.Close()
	os.Rename(tmp, s.path)
}
