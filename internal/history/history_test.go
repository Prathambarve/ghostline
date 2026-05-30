package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTempStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	return NewStore(path, 5000)
}

// writeRaw appends a record straight to disk, bypassing Append's redaction —
// used to simulate data persisted under an older, weaker secret policy.
func writeRaw(path string, r Record) error {
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func TestAppendDropsSecretsAndPersists(t *testing.T) {
	s := newTempStore(t)

	// denylisted — must not persist
	s.Append(Record{Command: "export TOKEN=abc123", ExitCode: 0, CWD: "/p"})
	// redacted — persists masked
	s.Append(Record{Command: "mysql --password hunter2", ExitCode: 0, CWD: "/p"})
	// normal
	s.Append(Record{Command: "git status", ExitCode: 0, CWD: "/p"})

	recs := s.Load()
	if len(recs) != 2 {
		t.Fatalf("expected 2 persisted records (secret dropped), got %d: %+v", len(recs), recs)
	}
	for _, r := range recs {
		if r.Command == "export TOKEN=abc123" {
			t.Errorf("secret command was persisted")
		}
		if r.Command == "mysql --password hunter2" {
			t.Errorf("secret value not redacted on disk: %q", r.Command)
		}
	}
}

func TestFrequentRanksByFrequencyThenRecency(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	add := func(cmd string, exit int, repo, cwd string, ts time.Time) {
		s.Append(Record{Command: cmd, ExitCode: exit, GitRepo: repo, CWD: cwd, Timestamp: ts})
	}

	// "make dev" run 3x in repo ghostline; "git push" 1x; a failure; an off-repo cmd
	add("make dev", 0, "ghostline", "/p", base)
	add("make dev", 0, "ghostline", "/p", base.Add(time.Minute))
	add("make dev", 0, "ghostline", "/p", base.Add(2*time.Minute))
	add("git push", 0, "ghostline", "/p", base.Add(3*time.Minute))
	add("rm typo", 1, "ghostline", "/p", base) // failed: excluded
	add("npm run", 0, "other", "/elsewhere", base)

	got := s.Frequent("ghostline", "/p", "go", 5)

	if len(got) != 2 {
		t.Fatalf("expected 2 relevant successful commands, got %d: %v", len(got), got)
	}
	if got[0] != "make dev" {
		t.Errorf("most frequent should rank first; got %v", got)
	}
	for _, c := range got {
		if c == "rm typo" {
			t.Errorf("failed command should be excluded")
		}
		if c == "npm run" {
			t.Errorf("off-context command should be excluded")
		}
	}
}

func TestFrequentMatchesByCwdWhenNoRepo(t *testing.T) {
	s := newTempStore(t)
	s.Append(Record{Command: "ssh deploy@box", ExitCode: 0, CWD: "/home/me", Timestamp: time.Now()})

	got := s.Frequent("", "/home/me", "", 5)
	if len(got) != 1 || got[0] != "ssh deploy@box" {
		t.Errorf("expected cwd match, got %v", got)
	}
}

func TestCompactScrubsNowDenylistedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := NewStore(path, 5000)

	// A clean record and a record carrying a provider key. We bypass Append's
	// filtering by writing directly, simulating a key stored under older rules.
	s.Append(Record{Command: "git status", ExitCode: 0, CWD: "/p"})
	if err := writeRaw(path, Record{Command: "echo gsk_EXAMPLEdummytoken00000000", CWD: "/p"}); err != nil {
		t.Fatal(err)
	}

	// Reopen → compaction re-applies the current secret policy.
	s2 := NewStore(path, 5000)
	for _, r := range s2.Load() {
		if r.Command != "git status" {
			t.Errorf("leaked secret not scrubbed on restart: %q", r.Command)
		}
	}
}

func TestCompactTrimsToMaxLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := NewStore(path, 10)
	for i := 0; i < 25; i++ {
		s.Append(Record{Command: "cmd", ExitCode: 0, CWD: "/p"})
	}
	// reopen → compaction runs in NewStore
	s2 := NewStore(path, 10)
	if recs := s2.Load(); len(recs) != 10 {
		t.Errorf("expected compaction to 10 lines, got %d", len(recs))
	}
}
