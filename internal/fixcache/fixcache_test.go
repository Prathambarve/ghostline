package fixcache

import (
	"fmt"
	"path/filepath"
	"testing"
)

func newTempStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recoveries.jsonl")
	return NewStore(path, 2000)
}

func TestLearnAndLookupRoundtrip(t *testing.T) {
	s := newTempStore(t)

	if e := s.Lookup("gti push", "command not found: gti", "ghostline"); e != nil {
		t.Fatalf("expected miss before learning, got %+v", e)
	}

	s.Learn("gti push", "command not found: gti", "ghostline", "git push", "typo in git")

	e := s.Lookup("gti push", "command not found: gti", "ghostline")
	if e == nil {
		t.Fatal("expected a cache hit after learning")
	}
	if e.Fix != "git push" || e.Why != "typo in git" {
		t.Errorf("wrong entry replayed: %+v", e)
	}
}

func TestKeyComponentsAreStrict(t *testing.T) {
	s := newTempStore(t)
	s.Learn("gti push", "command not found: gti", "ghostline", "git push", "")

	// Each key component must match.
	cases := []struct {
		name, cmd, stderr, repo string
	}{
		{"different cmd", "gti pull", "command not found: gti", "ghostline"},
		{"different stderr", "gti push", "totally different error", "ghostline"},
		{"different repo", "gti push", "command not found: gti", "other-repo"},
	}
	for _, c := range cases {
		if e := s.Lookup(c.cmd, c.stderr, c.repo); e != nil {
			t.Errorf("%s: expected miss, got %+v", c.name, e)
		}
	}
}

func TestStderrNormalizedBeforeHashing(t *testing.T) {
	s := newTempStore(t)
	s.Learn("foo", "  some   error\n", "repo", "bar", "")

	// Cosmetic whitespace differences must still hit.
	if e := s.Lookup("foo", "some error", "repo"); e == nil {
		t.Error("expected normalized stderr to match despite whitespace differences")
	}
}

func TestSecretsAreNotCached(t *testing.T) {
	s := newTempStore(t)

	// Denylisted command (carries a token) — must not be learned.
	s.Learn("deploy TOKEN=abc123", "boom", "repo", "deploy ok", "")
	if e := s.Lookup("deploy TOKEN=abc123", "boom", "repo"); e != nil {
		t.Error("a command carrying a secret must not be cached")
	}

	// A fix that would be redacted (inline password) must not be stored either,
	// since redaction would corrupt the runnable fix.
	s.Learn("mysql", "access denied", "repo", "mysql --password hunter2", "")
	if e := s.Lookup("mysql", "access denied", "repo"); e != nil {
		t.Error("a fix carrying a secret must not be cached")
	}
}

func TestMostRecentWins(t *testing.T) {
	s := newTempStore(t)
	s.Learn("foo", "err", "repo", "first fix", "")
	s.Learn("foo", "err", "repo", "second fix", "")

	e := s.Lookup("foo", "err", "repo")
	if e == nil || e.Fix != "second fix" {
		t.Errorf("expected most-recently learned fix to win, got %+v", e)
	}
}

func TestEmptyInputsAreNoOps(t *testing.T) {
	s := newTempStore(t)
	s.Learn("", "err", "repo", "fix", "")
	s.Learn("cmd", "err", "repo", "", "")
	if e := s.Lookup("", "err", "repo"); e != nil {
		t.Error("empty command lookup should miss")
	}
	if e := s.Lookup("cmd", "err", "repo"); e != nil {
		t.Error("empty-fix learn should not have persisted anything")
	}
}

func TestLearnPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recoveries.jsonl")
	s := NewStore(path, 2000)
	s.Learn("gti", "not found", "repo", "git", "typo")

	s2 := NewStore(path, 2000)
	if e := s2.Lookup("gti", "not found", "repo"); e == nil || e.Fix != "git" {
		t.Errorf("expected learned fix to survive reopen, got %+v", e)
	}
}

func TestCompactTrimsToMaxLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recoveries.jsonl")
	s := NewStore(path, 10)
	for i := 0; i < 25; i++ {
		s.Learn(fmt.Sprintf("cmd%d", i), "err", "repo", "fix", "") // distinct keys
	}
	s2 := NewStore(path, 10)
	if recs := s2.load(); len(recs) != 10 {
		t.Errorf("expected compaction to 10 lines, got %d", len(recs))
	}
}

// Re-learning the same fix for the same failure must not grow the file — this is
// the replay-then-rerun case (a known typo fixed again) that previously appended
// a duplicate line every time.
func TestLearnDedupesIdenticalFix(t *testing.T) {
	s := newTempStore(t)
	for i := 0; i < 50; i++ {
		s.Learn("clde", "command not found: clde", "", "claude", "learned from your earlier correction")
	}
	if recs := s.load(); len(recs) != 1 {
		t.Errorf("identical re-learns should collapse to 1 entry, got %d", len(recs))
	}
	if e := s.Lookup("clde", "command not found: clde", ""); e == nil || e.Fix != "claude" {
		t.Errorf("expected to replay claude, got %+v", e)
	}
}

// A *changed* fix for the same failure is recorded (most-recent wins), and
// compaction collapses the key's history to just the latest.
func TestLearnUpdatesChangedFix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recoveries.jsonl")
	s := NewStore(path, 2000)
	s.Learn("foo", "err", "", "fix-v1", "")
	s.Learn("foo", "err", "", "fix-v2", "")
	if e := s.Lookup("foo", "err", ""); e == nil || e.Fix != "fix-v2" {
		t.Fatalf("expected newest fix-v2, got %+v", e)
	}
	s2 := NewStore(path, 2000) // triggers startup compaction → dedupe by key
	if recs := s2.load(); len(recs) != 1 {
		t.Errorf("compaction should keep one entry per key, got %d", len(recs))
	}
	if e := s2.Lookup("foo", "err", ""); e == nil || e.Fix != "fix-v2" {
		t.Errorf("compaction must preserve the most-recent fix, got %+v", e)
	}
}

func TestEmptyRepoKeysConsistently(t *testing.T) {
	s := newTempStore(t)
	s.Learn("foo", "err", "", "bar", "")
	if e := s.Lookup("foo", "err", ""); e == nil {
		t.Error("expected empty-repo entry to match empty-repo lookup")
	}
	if e := s.Lookup("foo", "err", "somerepo"); e != nil {
		t.Error("empty-repo entry must not match a non-empty repo")
	}
}
