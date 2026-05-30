package history

// Hard-mode edge-case and limitation tests for history.Store.
// Run: go test ./internal/history/... -run TestHistoryBugs -v

import (
	"testing"
	"time"
)

func TestFrequentProjectTypeNotFiltered(t *testing.T) {
	// BUG / LIMITATION: Frequent() accepts a projectType parameter but the
	// internal relevant() function never checks it — commands from any project
	// type in the same repo are surfaced regardless. The param is dead code.
	// This test documents that "python" commands appear even when asking for
	// "go"-context suggestions in the same repo.

	s := newTempStore(t)
	base := time.Now()

	s.Append(Record{Command: "python manage.py migrate", ExitCode: 0, GitRepo: "myapp", CWD: "/app", ProjectType: "python", Timestamp: base})
	s.Append(Record{Command: "go build ./...", ExitCode: 0, GitRepo: "myapp", CWD: "/app", ProjectType: "go", Timestamp: base.Add(time.Minute)})

	// Ask for go-project commands — should ideally exclude python commands.
	got := s.Frequent("myapp", "/app", "go", 10)

	// Currently returns BOTH because projectType is not filtered.
	// Desired behavior would be to prefer same-projectType commands.
	t.Logf("Frequent('myapp', '/app', 'go', 10) returned %d commands: %v", len(got), got)

	if len(got) != 2 {
		t.Errorf("expected 2 commands (both project types returned — projectType filter not implemented), got %d: %v", len(got), got)
	}

	// Document the limitation: python command appears in a Go-context query.
	hasPython := false
	for _, c := range got {
		if c == "python manage.py migrate" {
			hasPython = true
		}
	}
	if !hasPython {
		// If this ever starts failing it means projectType filtering was added —
		// update the test to verify the desired filtering behavior.
		t.Logf("INFO: python command no longer returned for go context — projectType filtering may have been implemented")
	} else {
		t.Logf("LIMITATION CONFIRMED: 'python manage.py migrate' surfaces in a Go-context query (projectType param unused)")
	}
}

func TestFrequentEmptyContext(t *testing.T) {
	s := newTempStore(t)
	s.Append(Record{Command: "git status", ExitCode: 0, GitRepo: "foo", CWD: "/foo", Timestamp: time.Now()})

	// Neither repo nor cwd matches → should return nothing.
	got := s.Frequent("", "", "", 5)
	if len(got) != 0 {
		t.Errorf("Frequent with empty context should return nothing, got %v", got)
	}
}

func TestFrequentOnlySuccessfulCommandsSurfaced(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	s.Append(Record{Command: "make build", ExitCode: 0, GitRepo: "proj", CWD: "/p", Timestamp: base})
	s.Append(Record{Command: "make tset", ExitCode: 2, GitRepo: "proj", CWD: "/p", Timestamp: base.Add(time.Minute)})
	s.Append(Record{Command: "make test", ExitCode: 0, GitRepo: "proj", CWD: "/p", Timestamp: base.Add(2 * time.Minute)})

	got := s.Frequent("proj", "/p", "", 10)
	for _, c := range got {
		if c == "make tset" {
			t.Errorf("failed command 'make tset' (exit 2) should not appear in Frequent output")
		}
	}
	if len(got) != 2 {
		t.Errorf("expected 2 successful commands, got %d: %v", len(got), got)
	}
}

func TestFrequentRepoMatchTakesPriorityOverCWD(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	// Same repo but different CWD sub-directory.
	s.Append(Record{Command: "go test ./...", ExitCode: 0, GitRepo: "myrepo", CWD: "/repo/pkg", Timestamp: base})
	s.Append(Record{Command: "go build", ExitCode: 0, GitRepo: "myrepo", CWD: "/repo/cmd", Timestamp: base.Add(time.Minute)})
	// Different repo entirely.
	s.Append(Record{Command: "npm start", ExitCode: 0, GitRepo: "other", CWD: "/other", Timestamp: base.Add(2 * time.Minute)})

	// Ask from a third sub-directory of the same repo.
	got := s.Frequent("myrepo", "/repo/lib", "", 10)
	if len(got) != 2 {
		t.Errorf("expected 2 commands from the same repo, got %d: %v", len(got), got)
	}
	for _, c := range got {
		if c == "npm start" {
			t.Errorf("command from different repo should not appear: %q", c)
		}
	}
}

func TestAppendEmptyCommandSkipped(t *testing.T) {
	s := newTempStore(t)
	s.Append(Record{Command: "", ExitCode: 0, CWD: "/p"})
	s.Append(Record{Command: "   ", ExitCode: 0, CWD: "/p"})
	recs := s.Load()
	if len(recs) != 0 {
		t.Errorf("empty/whitespace commands should not be persisted, got %d records", len(recs))
	}
}

func TestFrequentLimitRespected(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	for i := 0; i < 20; i++ {
		// Unique commands so all get distinct entries.
		s.Append(Record{
			Command:   "echo cmd" + string(rune('a'+i)),
			ExitCode:  0,
			GitRepo:   "testrepo",
			CWD:       "/p",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}

	got := s.Frequent("testrepo", "/p", "", 5)
	if len(got) != 5 {
		t.Errorf("Frequent with limit=5 should return exactly 5 commands, got %d", len(got))
	}
}

func TestCompactPreservesNewestRecords(t *testing.T) {
	path := newTempStore(t).path // use temp path
	s := NewStore(path, 5)

	base := time.Now()
	cmds := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for i, c := range cmds {
		s.Append(Record{
			Command:   c,
			ExitCode:  0,
			CWD:       "/p",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}

	s2 := NewStore(path, 5)
	recs := s2.Load()
	if len(recs) != 5 {
		t.Fatalf("compact should keep last 5 records, got %d", len(recs))
	}
	// Newest 5 are d, e, f, g, h
	wantLast := "h"
	if recs[len(recs)-1].Command != wantLast {
		t.Errorf("last record after compact should be %q, got %q", wantLast, recs[len(recs)-1].Command)
	}
	wantFirst := "d"
	if recs[0].Command != wantFirst {
		t.Errorf("first record after compact should be %q (oldest kept), got %q", wantFirst, recs[0].Command)
	}
}
