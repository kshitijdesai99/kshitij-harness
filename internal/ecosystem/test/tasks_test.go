package ecosystem_test

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"kh/internal/ecosystem"
)

func warmTask(t *testing.T, s *ecosystem.Store, parent ecosystem.Agent, name string) (ecosystem.Agent, ecosystem.TaskLease) {
	t.Helper()
	a := claim(t, s, reserve(t, s, parent, name))
	p := identity(t)
	if err := s.EnableReuse(ctx, a.ID, p); err != nil {
		t.Fatal(err)
	}
	l, err := s.BeginInitialTask(ctx, a.ID, p, "initial\nprivate context")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, a.ID, p, l, "initial result", ecosystem.TaskTimings{ExecutionMS: 12}); err != nil {
		t.Fatal(err)
	}
	return a, l
}

func TestPendingTaskRollbackIsFenced(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	a, _ := warmTask(t, s, r, "rollback")
	p := identity(t)
	l, err := s.ReserveTask(ctx, r.ID, a.ID, "undelivered")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CancelTaskReservation(ctx, r.ID, a.ID, p, l); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimTask(ctx, a.ID, p, l); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("cancelled claim: %v", err)
	}
	if get(t, s, a.ID).State != ecosystem.Idle {
		t.Fatal("rollback left worker busy")
	}
	next, err := s.ReserveTask(ctx, r.ID, a.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimTask(ctx, a.ID, p, next); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelTaskReservation(ctx, r.ID, a.ID, p, next); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("rollback released accepted task: %v", err)
	}
	if err = s.FinishTask(ctx, a.ID, p, next, "result", ecosystem.TaskTimings{}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLeaseFencing(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	a, old := warmTask(t, s, r, "warm")
	p := identity(t)
	l, err := s.ReserveTask(ctx, r.ID, a.ID, "new task\nprivate details")
	if err != nil {
		t.Fatal(err)
	}
	if l.Generation != old.Generation+1 || l.ID == old.ID {
		t.Fatalf("invalid generation %+v -> %+v", old, l)
	}
	if got := get(t, s, a.ID); got.Task != "new task" || got.Conclusion != "" {
		t.Fatalf("shared metadata %+v", got)
	}
	// Heartbeats cannot erase a pending reservation or make the worker eligible.
	if err = s.Heartbeat(ctx, a.ID, p, ecosystem.Idle); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveTask(ctx, r.ID, a.ID, "duplicate"); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("duplicate reserve: %v", err)
	}
	if err = s.ExpireIdle(ctx, a.ID, p); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("pending expiry: %v", err)
	}
	if err = s.ClaimTask(ctx, a.ID, p, old); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("stale claim: %v", err)
	}
	wrong := p
	wrong.StartToken += "-wrong"
	if err = s.ClaimTask(ctx, a.ID, wrong, l); !errors.Is(err, ecosystem.ErrIdentity) {
		t.Fatalf("wrong process claim: %v", err)
	}
	if err = s.ClaimTask(ctx, a.ID, p, l); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimTask(ctx, a.ID, p, l); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("double claim: %v", err)
	}
	if err = s.FinishTask(ctx, a.ID, p, old, "stale", ecosystem.TaskTimings{}); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("stale finish: %v", err)
	}
	if err = s.FinishTask(ctx, a.ID, wrong, l, "wrong", ecosystem.TaskTimings{}); !errors.Is(err, ecosystem.ErrIdentity) {
		t.Fatalf("wrong process finish: %v", err)
	}
	timings := ecosystem.TaskTimings{StartupMS: 1, ContextMS: 2, ExecutionMS: 3, ReportMS: 4}
	if err = s.FinishTask(ctx, a.ID, p, l, "new result", timings); err != nil {
		t.Fatal(err)
	}
	status, err := s.TaskStatus(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Lease != l || status.Status != "completed" || status.Timings != timings || !status.Reusable {
		t.Fatalf("status %+v", status)
	}
	if err = s.FinishTask(ctx, a.ID, p, l, "overwrite", timings); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("double finish: %v", err)
	}
	if err = s.ExpireIdle(ctx, a.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveTask(ctx, r.ID, a.ID, "too late"); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("reserve stopping: %v", err)
	}
	if stats(t, s, r.ID).Live != 2 {
		t.Fatal("retirement prematurely released capacity")
	}
	if err = s.Heartbeat(ctx, a.ID, p, ecosystem.Idle); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("heartbeat revived retirement: %v", err)
	}
}

func TestTaskReservationAuthorization(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 8)
	a, _ := warmTask(t, s, r, "warm")
	other := claim(t, s, reserve(t, s, r, "manager"))
	if err := s.SetRole(ctx, other.ID, ecosystem.Manager); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveTask(ctx, other.ID, a.ID, "other parent"); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("other parent: %v", err)
	}
	if _, err := s.ReserveTask(ctx, a.ID, a.ID, "worker actor"); !errors.Is(err, ecosystem.ErrRole) {
		t.Fatalf("worker actor: %v", err)
	}
	if err := s.SetRole(ctx, a.ID, ecosystem.Manager); err != nil {
		t.Fatal(err)
	}
	child := claim(t, s, reserve(t, s, a, "child"))
	if err := s.SetRole(ctx, a.ID, ecosystem.Worker); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveTask(ctx, r.ID, a.ID, "has descendants"); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("descendants: %v", err)
	}
	if err := s.ExpireIdle(ctx, a.ID, identity(t)); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("expiry descendants: %v", err)
	}
	if err := s.ConfirmStopped(ctx, child.ID, identity(t), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveTask(ctx, r.ID, a.ID, "now eligible"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentTaskReservationsHaveOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s := open(t, path)
	other := open(t, path)
	r := root(t, s, 3)
	a, _ := warmTask(t, s, r, "warm")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, store := range []*ecosystem.Store{s, other} {
		wg.Add(1)
		go func(store *ecosystem.Store) {
			defer wg.Done()
			_, err := store.ReserveTask(ctx, r.ID, a.ID, "task")
			results <- err
		}(store)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ecosystem.ErrState) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("%d winners", success)
	}
}

func TestConcurrentTaskReservationAndRetirement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s := open(t, path)
	other := open(t, path)
	r := root(t, s, 3)
	a, _ := warmTask(t, s, r, "warm")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, err := s.ReserveTask(ctx, r.ID, a.ID, "one"); results <- err }()
	go func() { defer wg.Done(); results <- other.ExpireIdle(ctx, a.ID, identity(t)) }()
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ecosystem.ErrState) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("%d winners", success)
	}
}

func TestStoppedTaskCannotPublish(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	a := claim(t, s, reserve(t, s, r, "stopped"))
	p := identity(t)
	l, err := s.BeginInitialTask(ctx, a.ID, p, "task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Stop(ctx, r.ID, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, a.ID, p, l, "late result", ecosystem.TaskTimings{}); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("stopped finish: %v", err)
	}
	if got := get(t, s, a.ID); got.Conclusion != "" || got.State != ecosystem.Stopping {
		t.Fatalf("late mutation %+v", got)
	}
}

func TestTaskConclusionIsBounded(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	a := claim(t, s, reserve(t, s, r, "bounded"))
	p := identity(t)
	l, err := s.BeginInitialTask(ctx, a.ID, p, "task")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, a.ID, p, l, strings.Repeat("x", 17000), ecosystem.TaskTimings{}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, s, a.ID).Conclusion; len(got) > 16003 || !strings.HasSuffix(got, "…") {
		t.Fatalf("conclusion length %d", len(got))
	}
}

func TestInitialTaskDoesNotRequireReuse(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	a := claim(t, s, reserve(t, s, r, "ordinary"))
	p := identity(t)
	l, err := s.BeginInitialTask(ctx, a.ID, p, "initial")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginInitialTask(ctx, a.ID, p, "again"); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("duplicate initial: %v", err)
	}
	if err = s.FinishTask(ctx, a.ID, p, l, "result", ecosystem.TaskTimings{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveTask(ctx, r.ID, a.ID, "not reusable"); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("ordinary reserve: %v", err)
	}
	if err = s.ExpireIdle(ctx, a.ID, p); !errors.Is(err, ecosystem.ErrState) {
		t.Fatalf("ordinary expiry: %v", err)
	}
	if _, err = s.TaskStatus(ctx, "missing"); !errors.Is(err, ecosystem.ErrNotFound) {
		t.Fatalf("missing status: %v", err)
	}
}
