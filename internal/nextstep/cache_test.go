package nextstep

import (
	"fmt"
	"path/filepath"
	"testing"
)

func newTempStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "predictions.jsonl"), 2000)
}

func TestLearnAndLookup(t *testing.T) {
	s := newTempStore(t)
	if e := s.Lookup("terraform plan", "app"); e != nil {
		t.Fatalf("expected miss, got %+v", e)
	}
	s.Learn("terraform plan", "app", "terraform apply", true)
	e := s.Lookup("terraform plan", "app")
	if e == nil || e.Next != "terraform apply" || !e.Destructive {
		t.Fatalf("expected terraform apply (destructive), got %+v", e)
	}
	// Different repo must not match.
	if e := s.Lookup("terraform plan", "other"); e != nil {
		t.Errorf("repo isolation failed: %+v", e)
	}
}

func TestLearnDedupesIdentical(t *testing.T) {
	s := newTempStore(t)
	for i := 0; i < 30; i++ {
		s.Learn("git add -A", "app", "git commit", false)
	}
	if recs := s.load(); len(recs) != 1 {
		t.Errorf("identical re-learns should collapse to 1, got %d", len(recs))
	}
}

func TestLearnUpdatesChangedPrediction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "predictions.jsonl")
	s := NewStore(path, 2000)
	s.Learn("make", "app", "make test", false)
	s.Learn("make", "app", "make build", false)
	if e := s.Lookup("make", "app"); e == nil || e.Next != "make build" {
		t.Fatalf("expected newest prediction make build, got %+v", e)
	}
	s2 := NewStore(path, 2000) // startup compaction dedupes by key
	if recs := s2.load(); len(recs) != 1 {
		t.Errorf("compaction should keep one entry per key, got %d", len(recs))
	}
}

func TestCompactTrimsToMaxLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "predictions.jsonl")
	s := NewStore(path, 10)
	for i := 0; i < 25; i++ {
		s.Learn(fmt.Sprintf("cmd%d", i), "app", "next", false) // distinct keys
	}
	if recs := NewStore(path, 10).load(); len(recs) != 10 {
		t.Errorf("expected trim to 10, got %d", len(recs))
	}
}

func TestSecretsNotCached(t *testing.T) {
	s := newTempStore(t)
	s.Learn("deploy --token=ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "app", "next", false)
	if e := s.Lookup("deploy --token=ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "app"); e != nil {
		t.Errorf("secret-bearing command must not be cached, got %+v", e)
	}
}
