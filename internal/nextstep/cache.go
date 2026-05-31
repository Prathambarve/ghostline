package nextstep

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prathamesh/ghostline/internal/history"
)

// Entry is one learned prediction. The lookup key is (Cmd, Repo) — given the same
// command in the same repo, the next step is the same. Seeded by the model the
// first time, then replayed instantly and offline.
type Entry struct {
	Cmd         string    `json:"cmd"`
	Repo        string    `json:"repo"`
	Next        string    `json:"next"`
	Destructive bool      `json:"destructive"`
	Timestamp   time.Time `json:"ts"`
}

// Store is an append-only JSONL cache of predictions, mirroring internal/fixcache:
// dedupe-on-write, dedupe+trim on compaction, secret-gated, bounded.
type Store struct {
	mu       sync.Mutex
	path     string
	maxLines int
	appended int
}

func NewStore(path string, maxLines int) *Store {
	s := &Store{path: path, maxLines: maxLines}
	s.compact()
	return s
}

// secretFree reports whether s can be stored verbatim (no detectable secret under
// the history redaction policy). Reused from history so the cache never holds
// credentials and every cached command stays runnable.
func secretFree(s string) bool {
	cleaned, ok := history.Clean(s)
	return ok && cleaned == s
}

// Learn records a prediction for (cmd, repo). No-op on empty/secret-bearing input.
func (s *Store) Learn(cmd, repo, next string, destructive bool) error {
	cmd = strings.TrimSpace(cmd)
	next = strings.TrimSpace(next)
	if cmd == "" || next == "" {
		return nil
	}
	if !secretFree(cmd) || !secretFree(next) {
		return nil
	}

	e := Entry{Cmd: cmd, Repo: repo, Next: next, Destructive: destructive, Timestamp: time.Now()}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Skip a redundant append: same key already predicts the same next step.
	if latest := latestForKey(s.loadLocked(), e.Cmd, e.Repo); latest != nil && latest.Next == e.Next {
		return nil
	}
	if err := s.appendLocked(e); err != nil {
		return err
	}
	s.appended++
	if s.appended >= s.maxLines {
		s.compactLocked()
	}
	return nil
}

// Lookup returns the most-recently learned prediction for an exact (cmd, repo)
// match, or nil.
func (s *Store) Lookup(cmd, repo string) *Entry {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	return latestForKey(s.load(), cmd, repo)
}

func (s *Store) appendLocked(e Entry) error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

func latestForKey(recs []Entry, cmd, repo string) *Entry {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Cmd == cmd && recs[i].Repo == repo {
			return &recs[i]
		}
	}
	return nil
}

func key(e Entry) string { return e.Cmd + "\x00" + e.Repo }

func (s *Store) load() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() []Entry {
	f, err := os.Open(s.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var recs []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err == nil && e.Cmd != "" && e.Next != "" {
			recs = append(recs, e)
		}
	}
	return recs
}

func (s *Store) compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactLocked()
}

// compactLocked collapses to one entry per key (most-recent wins, matching
// Lookup) and trims to the last maxLines. Caller holds s.mu.
func (s *Store) compactLocked() {
	s.appended = 0
	if s.maxLines <= 0 {
		return
	}

	recs := s.loadLocked()
	orig := len(recs)

	seen := make(map[string]bool, len(recs))
	deduped := make([]Entry, 0, len(recs))
	for i := len(recs) - 1; i >= 0; i-- {
		k := key(recs[i])
		if seen[k] {
			continue
		}
		seen[k] = true
		deduped = append(deduped, recs[i])
	}
	for i, j := 0, len(deduped)-1; i < j; i, j = i+1, j-1 {
		deduped[i], deduped[j] = deduped[j], deduped[i]
	}
	recs = deduped

	trimmed := false
	if len(recs) > s.maxLines {
		recs = recs[len(recs)-s.maxLines:]
		trimmed = true
	}
	if len(recs) == orig && !trimmed {
		return
	}

	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for _, e := range recs {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		w.Write(append(line, '\n'))
	}
	w.Flush()
	f.Close()
	os.Rename(tmp, s.path)
}
