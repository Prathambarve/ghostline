package fixcache

import (
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
		s.Learn("cmd", "err", "repo", "fix", "")
	}
	s2 := NewStore(path, 10)
	if recs := s2.load(); len(recs) != 10 {
		t.Errorf("expected compaction to 10 lines, got %d", len(recs))
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
