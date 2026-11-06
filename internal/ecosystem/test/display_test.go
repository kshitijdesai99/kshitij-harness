package ecosystem_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/ecosystem"
)

func TestDisplayAssignmentsImmutableAndSafe(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 5)
	launch := "exact launch\n秘密\nPRIVATE_DISPLAY"
	reservation, err := s.Reserve(ctx, r.ID, "display", "private summary", launch)
	if err != nil {
		t.Fatal(err)
	}
	a := claim(t, s, reservation)
	p := identity(t)
	if err = s.EnableReuse(ctx, a.ID, p); err != nil {
		t.Fatal(err)
	}
	first, err := s.BeginInitialTask(ctx, a.ID, p, "not the assignment")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, a.ID, p, first, "first", ecosystem.TaskTimings{}); err != nil {
		t.Fatal(err)
	}
	secondTask := strings.Repeat("界", 21845) + "x" // exactly 64 KiB
	second, err := s.ReserveTask(ctx, r.ID, a.ID, "second summary", secondTask)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimTask(ctx, a.ID, p, second); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelTaskReservation(ctx, r.ID, a.ID, p, second); err == nil {
		t.Fatal("accepted task cancelled")
	}
	if err = s.FinishTask(ctx, a.ID, p, second, "second", ecosystem.TaskTimings{}); err != nil {
		t.Fatal(err)
	}
	if err = s.PublishResult(ctx, a.ID, "ordinary", "ordinary"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DisplayResultsAfter(ctx, r.ID, 0, 100)
	if err != nil || len(rows) != 3 {
		t.Fatalf("%+v %v", rows, err)
	}
	for i, want := range []string{launch, secondTask, launch} {
		if rows[i].Task == nil || *rows[i].Task != want {
			t.Fatalf("task %d mismatch", i)
		}
	}
	safe, err := s.ResultsAfter(ctx, r.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(safe)
	if strings.Contains(string(b), "PRIVATE_DISPLAY") {
		t.Fatal("unsafe result feed")
	}
	page, err := s.DisplayResultsAfter(ctx, r.ID, rows[0].Seq, 1)
	if err != nil || len(page) != 1 || page[0].Seq != rows[1].Seq {
		t.Fatal("pagination", err)
	}
	other := root(t, s, 3)
	isolated, err := s.DisplayResultsAfter(ctx, other.ID, 0, 100)
	if err != nil || len(isolated) != 0 {
		t.Fatal("scope", err)
	}
	for _, args := range [][]string{{"a", "b"}, {strings.Repeat("x", 65537)}} {
		if _, err = s.ReserveTask(ctx, r.ID, a.ID, "bad", args...); err == nil {
			t.Fatal("invalid capture accepted")
		}
		if get(t, s, a.ID).State != ecosystem.Idle {
			t.Fatal("invalid capture mutated state")
		}
		if _, err = s.Reserve(ctx, r.ID, "invalid", "bad", args...); err == nil {
			t.Fatal("invalid launch accepted")
		}
	}
	cancel, err := s.ReserveTask(ctx, r.ID, a.ID, "cancel", "discard")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CancelTaskReservation(ctx, r.ID, a.ID, p, cancel); err != nil {
		t.Fatal(err)
	}
	final, err := s.ReserveTask(ctx, r.ID, a.ID, "PRIVATE_SENTINEL")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimTask(ctx, a.ID, p, final); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, a.ID, p, final, "missing", ecosystem.TaskTimings{}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.DisplayResultsAfter(ctx, r.ID, rows[2].Seq, 100)
	if err != nil || len(rows) != 1 || rows[0].Task != nil {
		t.Fatal("implicit summary captured", err)
	}
}

func TestDisplayOldDatabaseAdditiveOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s := open(t, path)
	r := root(t, s, 4)
	a := claim(t, s, reserve(t, s, r, "old"))
	if err := s.PublishResult(ctx, a.ID, "old-result", "old conclusion"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP TABLE agent_display_tasks`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := open(t, path)
	rows, err := reopened.DisplayResultsAfter(ctx, r.ID, 0, 100)
	if err != nil || len(rows) != 1 || rows[0].Task != nil || rows[0].Conclusion != "old conclusion" {
		t.Fatalf("legacy rows %+v: %v", rows, err)
	}
}

func TestDisplayMissingAndLaunchRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s := open(t, path)
	r := root(t, s, 8)
	a := claim(t, s, reserve(t, s, r, "legacy"))
	if err := s.PublishResult(ctx, a.ID, "legacy", "done"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DisplayResultsAfter(ctx, r.ID, 0, 100)
	if err != nil || len(rows) != 1 || rows[0].Task != nil {
		t.Fatal("missing assignment", err)
	}
	pending, err := s.Reserve(ctx, r.ID, "cancelled", "summary", "task")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CancelReservation(ctx, pending.Agent.ID, pending.Token); err != nil {
		t.Fatal(err)
	}
	failed, err := s.Reserve(ctx, r.ID, "failed", "summary", "task")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginLaunch(ctx, failed.Agent.ID, failed.Token, identity(t)); err != nil {
		t.Fatal(err)
	}
	if err = s.FailLaunch(ctx, failed.Agent.ID, failed.Token, false); err == nil {
		t.Fatal("unknown launch released")
	}
	if err = s.FailLaunch(ctx, failed.Agent.ID, failed.Token, true); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Reserve(ctx, r.ID, "accepted", "summary", "kept")
	if err != nil {
		t.Fatal(err)
	}
	child := claim(t, s, accepted)
	if err = s.FailLaunch(ctx, child.ID, accepted.Token, true); err == nil {
		t.Fatal("accepted launch released")
	}
	if err = s.PublishResult(ctx, child.ID, "published", "done"); err != nil {
		t.Fatal(err)
	}
	rows, err = s.DisplayResultsAfter(ctx, r.ID, rows[0].Seq, 100)
	if err != nil || len(rows) != 1 || rows[0].Task == nil || *rows[0].Task != "kept" {
		t.Fatal("accepted snapshot lost", err)
	}
}
