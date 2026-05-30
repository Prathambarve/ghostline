package workflow

import (
	"path/filepath"
	"testing"
)

func newTempStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "workflows.yaml"))
}

func TestAddListGetRemove(t *testing.T) {
	s := newTempStore(t)

	if got := s.List(); len(got) != 0 {
		t.Fatalf("expected empty store, got %v", got)
	}

	if err := s.Add(Workflow{Name: "deploy", Command: "make build && make deploy", Description: "ship it"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Workflow{Name: "logs", Command: "kubectl logs -f"}); err != nil {
		t.Fatal(err)
	}

	// List is sorted by name.
	ws := s.List()
	if len(ws) != 2 || ws[0].Name != "deploy" || ws[1].Name != "logs" {
		t.Fatalf("unexpected list: %+v", ws)
	}

	w, ok := s.Get("deploy")
	if !ok || w.Command != "make build && make deploy" || w.Description != "ship it" {
		t.Fatalf("Get returned %+v ok=%v", w, ok)
	}

	removed, err := s.Remove("deploy")
	if err != nil || !removed {
		t.Fatalf("Remove returned removed=%v err=%v", removed, err)
	}
	if _, ok := s.Get("deploy"); ok {
		t.Error("deploy should be gone after Remove")
	}
	if removed, _ := s.Remove("missing"); removed {
		t.Error("removing a missing workflow should report false")
	}
}

func TestAddUpserts(t *testing.T) {
	s := newTempStore(t)
	s.Add(Workflow{Name: "x", Command: "echo one"})
	s.Add(Workflow{Name: "x", Command: "echo two"})

	ws := s.List()
	if len(ws) != 1 || ws[0].Command != "echo two" {
		t.Fatalf("expected upsert to a single 'echo two', got %+v", ws)
	}
}

func TestAddRejectsEmpty(t *testing.T) {
	s := newTempStore(t)
	if err := s.Add(Workflow{Name: "", Command: "x"}); err == nil {
		t.Error("expected error for empty name")
	}
	if err := s.Add(Workflow{Name: "x", Command: ""}); err == nil {
		t.Error("expected error for empty command")
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflows.yaml")
	NewStore(path).Add(Workflow{Name: "a", Command: "ls -la"})

	if w, ok := NewStore(path).Get("a"); !ok || w.Command != "ls -la" {
		t.Errorf("expected workflow to persist, got %+v ok=%v", w, ok)
	}
}

func TestExpand(t *testing.T) {
	cases := []struct {
		command string
		values  map[string]string
		want    string
	}{
		{"ssh {{host}}", map[string]string{"host": "box1"}, "ssh box1"},
		{"deploy {{env}} {{ region }}", map[string]string{"env": "prod", "region": "us"}, "deploy prod us"},
		{"ssh {{host}}", nil, "ssh {{host}}"}, // unprovided placeholders left intact
		{"echo hi", map[string]string{"x": "y"}, "echo hi"},
	}
	for _, c := range cases {
		if got := Expand(c.command, c.values); got != c.want {
			t.Errorf("Expand(%q, %v) = %q, want %q", c.command, c.values, got, c.want)
		}
	}
}
