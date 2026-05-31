// Package fixcache persists user-accepted error recoveries so that an identical
// failure can be fixed instantly, offline, with no API call. A recovery is
// "accepted" when the user runs the suggested fix and it succeeds; that maps
// (failed command, stderr, repo) → (fix, why) for future replay. This is the
// learning half of error recovery: every accepted fix makes the next identical
// failure free and instant.
//
// Privacy:
//   - Raw stderr is NEVER written to disk. Only a SHA-256 hash of the normalized
//     stderr is stored, as part of the lookup key — consistent with Ghostline's
//     rule that stderr never touches disk.
//   - Commands and fixes are stored verbatim, but only when they carry no
//     detectable secret (see history.Clean). Anything that would be redacted or
//     denylisted is skipped entirely — so the file never holds credentials, and
//     every stored fix stays runnable as-is (redaction would corrupt it).
package fixcache

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prathamesh/ghostline/internal/history"
)

// Entry is one learned recovery. The lookup key is (Cmd, StderrHash, Repo); Fix
// and Why are what we replay.
type Entry struct {
	Cmd        string    `json:"cmd"`
	StderrHash string    `json:"stderr_hash"`
	Repo       string    `json:"repo"`
	Fix        string    `json:"fix"`
	Why        string    `json:"why"`
	Timestamp  time.Time `json:"ts"`
}

type Store struct {
	mu       sync.Mutex
	path     string
	maxLines int
	appended int // appends since the last compaction (guarded by mu)
}

// NewStore opens (and compacts) the recovery cache at path, keeping at most
// maxLines most-recent entries.
func NewStore(path string, maxLines int) *Store {
	s := &Store{path: path, maxLines: maxLines}
	s.compact()
	return s
}

// hashStderr returns the key form of stderr: a hash of its normalized text. The
// raw stderr is never stored, only this digest.
func hashStderr(stderr string) string {
	sum := sha256.Sum256([]byte(normalize(stderr)))
	return hex.EncodeToString(sum[:])
}

// normalize collapses whitespace runs and trims, so cosmetically-different but
// semantically-identical stderr (extra spaces, trailing newline) keys the same.
func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// secretFree reports whether s can be stored verbatim — i.e. it carries no
// detectable secret under the history redaction policy. We require the cleaned
// form to equal the input: if Clean would drop (denylist) or alter (redact) it,
// the command isn't safe to store as a runnable fix.
func secretFree(s string) bool {
	cleaned, ok := history.Clean(s)
	return ok && cleaned == s
}

// Learn records an accepted fix for (cmd, stderr, repo). It is a deliberate
// no-op (returning nil) when either the failed command or the fix is empty or
// carries a secret — callers treat the cache as best-effort.
func (s *Store) Learn(cmd, stderr, repo, fix, why string) error {
	cmd = strings.TrimSpace(cmd)
	fix = strings.TrimSpace(fix)
	if cmd == "" || fix == "" {
		return nil
	}
	if !secretFree(cmd) || !secretFree(fix) {
		return nil
	}

	e := Entry{
		Cmd:        cmd,
		StderrHash: hashStderr(stderr),
		Repo:       repo,
		Fix:        fix,
		Why:        why,
		Timestamp:  time.Now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Skip a redundant append: if the newest entry for this key already replays
	// the same fix, re-learning it (e.g. after re-running a previously learned
	// fix) would only bloat the file. A *different* fix is still appended so the
	// most-recent correction wins.
	if latest := latestForKey(s.loadLocked(), e.Cmd, e.StderrHash, e.Repo); latest != nil && latest.Fix == e.Fix {
		return nil
	}

	if err := s.appendLocked(e); err != nil {
		return err
	}
	s.appended++
	// Safety net for a daemon that never restarts: once we've appended a cap's
	// worth, compact in place (dedupes by key and trims) so the file can't grow
	// unbounded between restarts.
	if s.appended >= s.maxLines {
		s.compactLocked()
	}
	return nil
}

// appendLocked writes one entry; caller holds s.mu.
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

// latestForKey returns the most-recent entry matching the (cmd, stderr-hash,
// repo) key, or nil. recs is oldest-first.
func latestForKey(recs []Entry, cmd, stderrHash, repo string) *Entry {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Cmd == cmd && recs[i].StderrHash == stderrHash && recs[i].Repo == repo {
			return &recs[i]
		}
	}
	return nil
}

// key is the dedupe identity of an entry: same key ⇒ same lookup, so only the
// most-recent one need be kept.
func key(e Entry) string {
	return e.Cmd + "\x00" + e.StderrHash + "\x00" + e.Repo
}

// Lookup returns the most-recently learned fix for an exact (cmd, stderr, repo)
// match, or nil. Most-recent wins so a fix the user re-learned (corrected)
// supersedes an older one.
func (s *Store) Lookup(cmd, stderr, repo string) *Entry {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	target := hashStderr(stderr)

	recs := s.load()
	var best *Entry
	for i := range recs {
		e := &recs[i]
		if e.Cmd == cmd && e.StderrHash == target && e.Repo == repo {
			best = e // later entries override earlier ones (file is oldest-first)
		}
	}
	return best
}

// load reads all entries from disk, oldest first. Malformed lines are skipped.
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
		if err := json.Unmarshal(sc.Bytes(), &e); err == nil && e.Cmd != "" && e.Fix != "" {
			recs = append(recs, e)
		}
	}
	return recs
}

// compact dedupes and trims the file. Called on startup and as a runtime safety
// net; bounds growth without a background process.
func (s *Store) compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactLocked()
}

// compactLocked collapses the file to one entry per key (most-recent wins, which
// matches Lookup) and trims to the last maxLines. Caller holds s.mu. It resets
// the append counter and only rewrites when something actually changed.
func (s *Store) compactLocked() {
	s.appended = 0
	if s.maxLines <= 0 {
		return
	}

	recs := s.loadLocked()
	orig := len(recs)

	// Keep only the most-recent entry per key. Walk newest→oldest, emit first
	// sighting of each key, then restore oldest-first order.
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
		return // no duplicates and under the cap — nothing to rewrite
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
