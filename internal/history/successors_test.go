package history

import (
	"testing"
	"time"
)

// helper: append a record with explicit timestamp so transition windows are
// deterministic (Append stamps now() when Timestamp is zero).
func addRec(s *Store, cmd string, exit int, repo, cwd string, ts time.Time) {
	s.Append(Record{Command: cmd, ExitCode: exit, GitRepo: repo, CWD: cwd, Timestamp: ts})
}

func TestSuccessorsRanksByFrequency(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	// "git add -A" → "git commit" twice, → "git status" once.
	addRec(s, "git add -A", 0, "ghostline", "/p", base)
	addRec(s, "git commit", 0, "ghostline", "/p", base.Add(time.Second))
	addRec(s, "git add -A", 0, "ghostline", "/p", base.Add(time.Minute))
	addRec(s, "git commit", 0, "ghostline", "/p", base.Add(time.Minute+time.Second))
	addRec(s, "git add -A", 0, "ghostline", "/p", base.Add(2*time.Minute))
	addRec(s, "git status", 0, "ghostline", "/p", base.Add(2*time.Minute+time.Second))

	got := s.Successors("git add -A", "ghostline", "/p", 5)
	if len(got) != 2 {
		t.Fatalf("expected 2 successors, got %d: %v", len(got), got)
	}
	if got[0] != "git commit" {
		t.Errorf("most frequent successor should rank first; got %v", got)
	}
}

func TestSuccessorsOnlySuccessful(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	// the command that followed failed — must not be suggested.
	addRec(s, "make build", 0, "ghostline", "/p", base)
	addRec(s, "make deploy", 1, "ghostline", "/p", base.Add(time.Second))

	if got := s.Successors("make build", "ghostline", "/p", 5); len(got) != 0 {
		t.Errorf("failed successor should be excluded, got %v", got)
	}
}

func TestSuccessorsRespectsTimeWindow(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	// next command logged 30 min later — likely a different session, excluded.
	addRec(s, "vim main.go", 0, "ghostline", "/p", base)
	addRec(s, "go build ./...", 0, "ghostline", "/p", base.Add(30*time.Minute))

	if got := s.Successors("vim main.go", "ghostline", "/p", 5); len(got) != 0 {
		t.Errorf("successor outside the time window should be excluded, got %v", got)
	}
}

func TestSuccessorsRequiresContext(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	// the transition happened in a different repo — not relevant to ghostline.
	addRec(s, "terraform plan", 0, "other", "/elsewhere", base)
	addRec(s, "terraform apply", 0, "other", "/elsewhere", base.Add(time.Second))

	if got := s.Successors("terraform plan", "ghostline", "/p", 5); len(got) != 0 {
		t.Errorf("off-context transition should be excluded, got %v", got)
	}
}

func TestSuccessorsSkipsSelfRepeat(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	// running the same command twice in a row is not a useful "next step".
	addRec(s, "ls", 0, "ghostline", "/p", base)
	addRec(s, "ls", 0, "ghostline", "/p", base.Add(time.Second))

	if got := s.Successors("ls", "ghostline", "/p", 5); len(got) != 0 {
		t.Errorf("a self-repeat should not be suggested, got %v", got)
	}
}

func TestSuccessorsEmptyPrev(t *testing.T) {
	s := newTempStore(t)
	addRec(s, "git status", 0, "ghostline", "/p", time.Now())
	if got := s.Successors("", "ghostline", "/p", 5); got != nil {
		t.Errorf("empty prev should return nil, got %v", got)
	}
}

func TestSuccessorsLimitRespected(t *testing.T) {
	s := newTempStore(t)
	base := time.Now()

	// "deploy" is followed by 3 distinct commands; limit to 2.
	for i, next := range []string{"a", "b", "c"} {
		ts := base.Add(time.Duration(i) * time.Minute)
		addRec(s, "deploy", 0, "ghostline", "/p", ts)
		addRec(s, next, 0, "ghostline", "/p", ts.Add(time.Second))
	}
	if got := s.Successors("deploy", "ghostline", "/p", 2); len(got) != 2 {
		t.Errorf("limit not respected, got %d: %v", len(got), got)
	}
}
