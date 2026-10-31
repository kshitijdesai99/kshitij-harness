package ecosystem_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"kh/internal/ecosystem"
)

var ctx = context.Background()

func open(t *testing.T, path string) *ecosystem.Store {
	t.Helper()
	s, e := ecosystem.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func identity(t *testing.T) ecosystem.ProcessIdentity {
	t.Helper()
	p, e := ecosystem.CurrentProcess()
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func root(t *testing.T, s *ecosystem.Store, max int) ecosystem.Agent {
	t.Helper()
	a, e := s.CreateRoot(ctx, ecosystem.RootOptions{MaxAgents: max, Process: identity(t), Pane: ecosystem.PaneInfo{SessionID: "$1", PaneID: "%1"}})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func reserve(t *testing.T, s *ecosystem.Store, parent ecosystem.Agent, name string) ecosystem.Reservation {
	t.Helper()
	r, e := s.Reserve(ctx, parent.ID, name, "task "+name)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func claim(t *testing.T, s *ecosystem.Store, r ecosystem.Reservation) ecosystem.Agent {
	t.Helper()
	p := identity(t)
	if e := s.BeginLaunch(ctx, r.Agent.ID, r.Token, p); e != nil {
		t.Fatal(e)
	}
	a, e := s.Claim(ctx, r.Agent.ID, r.Token, p, ecosystem.PaneInfo{PaneID: "%2"})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func get(t *testing.T, s *ecosystem.Store, id string) ecosystem.Agent {
	t.Helper()
	a, e := s.Get(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func stats(t *testing.T, s *ecosystem.Store, id string) ecosystem.Stats {
	t.Helper()
	a, e := s.Stats(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	return a
}

func TestRegistryFileIsPrivate(t *testing.T) {
	for _, setup := range []string{"new", "existing", "existing-sidecars"} {
		t.Run(setup, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "registry.db")
			if setup == "existing" {
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if setup == "existing-sidecars" {
				// Keep a connection open so SQLite retains valid WAL/SHM files.
				root(t, open(t, path), 2)
			}
			if setup != "new" {
				paths := []string{path}
				if setup == "existing-sidecars" {
					paths = append(paths, path+"-wal", path+"-shm")
				}
				for _, file := range paths {
					if err := os.Chmod(file, 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			s := open(t, path)
			root(t, s, 2)
			// Check before cleanup: closing SQLite may remove its sidecars.
			for _, file := range []string{path, path + "-wal", path + "-shm"} {
				info, err := os.Stat(file)
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != 0600 {
					t.Errorf("%s permissions %04o, want 0600", filepath.Base(file), got)
				}
			}
		})
	}
}

func TestLowerCapacityDoesNotEvict(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	child := claim(t, s, reserve(t, s, r, "child"))
	pending := reserve(t, s, r, "pending")
	if err := s.SetMax(ctx, r.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := stats(t, s, r.ID); got.MaxAgents != 1 || got.Live != 3 || got.Available != 0 {
		t.Fatalf("lowered capacity %+v", got)
	}
	if _, err := s.Reserve(ctx, r.ID, "over", ""); !errors.Is(err, ecosystem.ErrCapacity) {
		t.Fatalf("reserve above lowered capacity: %v", err)
	}
	for _, want := range []ecosystem.Agent{r, child, pending.Agent} {
		if got := get(t, s, want.ID); got.State != want.State || got.Process != want.Process {
			t.Fatalf("agent changed after lowering capacity: got %+v, want %+v", got, want)
		}
	}
}

func TestDefaultsAndValidation(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 0)
	if got := stats(t, s, r.ID); got.MaxAgents != 50 || got.Live != 1 || got.Available != 49 {
		t.Fatalf("defaults %+v", got)
	}
	if _, e := s.CreateRoot(ctx, ecosystem.RootOptions{}); !errors.Is(e, ecosystem.ErrIdentity) {
		t.Fatalf("missing root identity %v", e)
	}
	for _, name := range []string{"", "bad:name", "path/name", "spaces disallowed"} {
		if _, e := s.Reserve(ctx, r.ID, name, ""); e == nil {
			t.Fatalf("accepted name %q", name)
		}
	}
	if e := s.SetRole(ctx, r.ID, "unknown"); e == nil {
		t.Fatal("unknown role accepted")
	}
	if e := s.SetMax(ctx, r.ID, 0); e == nil {
		t.Fatal("zero max accepted")
	}
	if _, e := s.Stats(ctx, "missing"); !errors.Is(e, ecosystem.ErrNotFound) {
		t.Fatalf("missing stats %v", e)
	}
	if e := s.Heartbeat(ctx, r.ID, ecosystem.ProcessIdentity{PID: r.Process.PID, StartToken: "other"}, ecosystem.Idle); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("other incarnation heartbeat %v", e)
	}
	if e := s.Heartbeat(ctx, r.ID, r.Process, ecosystem.Idle); e != nil {
		t.Fatal(e)
	}
	if got := stats(t, s, r.ID); got.States[ecosystem.Idle] != 1 || got.Live != 1 {
		t.Fatalf("idle slot %+v", got)
	}
}

func TestSharedTaskMetadataDiscardsPrivateContext(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	const private = "PRIVATE_MULTILINE_CONTEXT_SENTINEL"
	r, e := s.CreateRoot(ctx, ecosystem.RootOptions{Process: identity(t), Task: "  root   summary \n" + private})
	if e != nil {
		t.Fatal(e)
	}
	if r.Task != "root summary" {
		t.Fatalf("root task %q", r.Task)
	}
	reservation, e := s.Reserve(ctx, r.ID, "summary", "Child summary\n"+private)
	if e != nil {
		t.Fatal(e)
	}
	if reservation.Agent.Task != "Child summary" {
		t.Fatalf("child task %q", reservation.Agent.Task)
	}
	bounded, e := s.Reserve(ctx, r.ID, "unicode", strings.Repeat("界", 400)+"\n"+private)
	if e != nil {
		t.Fatal(e)
	}
	if !utf8.ValidString(bounded.Agent.Task) || utf8.RuneCountInString(bounded.Agent.Task) != 280 {
		t.Fatalf("invalid bounded summary %q", bounded.Agent.Task)
	}
	agents, e := s.List(ctx, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := json.Marshal(agents)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded), private) || !strings.Contains(string(encoded), "task_summary") {
		t.Fatalf("private task context exposed %s", encoded)
	}
}

func TestCancelledAdmissionDoesNotLeakSlot(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 2)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := s.Reserve(cancelled, r.ID, "cancelled", ""); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancelled reserve %v", e)
	}
	if got := stats(t, s, r.ID); got.Live != 1 {
		t.Fatalf("cancelled reservation committed %+v", got)
	}
	reserve(t, s, r, "still-available")
}

func TestRolesAndReusableLiveCapacity(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	if r.Role != ecosystem.Manager || r.ParentID != "" {
		t.Fatalf("root %+v", r)
	}
	if e := s.SetRole(ctx, r.ID, ecosystem.Worker); e == nil {
		t.Fatal("root demoted")
	}
	child := claim(t, s, reserve(t, s, r, "child"))
	if child.Role != ecosystem.Worker {
		t.Fatal("child not worker")
	}
	if _, e := s.Reserve(ctx, child.ID, "grand", "task"); !errors.Is(e, ecosystem.ErrRole) {
		t.Fatalf("worker spawned: %v", e)
	}
	if e := s.SetRole(ctx, child.ID, ecosystem.Manager); e != nil {
		t.Fatal(e)
	}
	grand := reserve(t, s, child, "grand")
	if _, e := s.Reserve(ctx, r.ID, "over", "task"); !errors.Is(e, ecosystem.ErrCapacity) {
		t.Fatalf("capacity: %v", e)
	}
	st := stats(t, s, r.ID)
	if st.Live != 3 || st.Managers != 2 || st.Workers != 1 || st.Available != 0 {
		t.Fatalf("stats %+v", st)
	}
	if e := s.CancelReservation(ctx, grand.Agent.ID, grand.Token); e != nil {
		t.Fatal(e)
	}
	replacement := reserve(t, s, child, "grand")
	if replacement.Agent.ID == grand.Agent.ID {
		t.Fatal("stable ID reused")
	}
	resolved, e := s.Resolve(ctx, r.ID, "kh:grand")
	if e != nil || resolved.ID != replacement.Agent.ID {
		t.Fatalf("name lookup %+v %v", resolved, e)
	}
	old, e := s.Resolve(ctx, r.ID, grand.Agent.Address)
	if e != nil || old.ID != grand.Agent.ID {
		t.Fatalf("stable lookup %+v %v", old, e)
	}
	if e = s.SetRole(ctx, child.ID, ecosystem.Worker); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reserve(ctx, child.ID, "other", ""); !errors.Is(e, ecosystem.ErrRole) {
		t.Fatalf("demoted manager spawned %v", e)
	}
}

func TestLaunchFencingAndConservativeRollback(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 2)
	p := identity(t)
	a := reserve(t, s, r, "cancelled")
	if e := s.CancelReservation(ctx, a.Agent.ID, "wrong"); !errors.Is(e, ecosystem.ErrState) {
		t.Fatal(e)
	}
	if e := s.CancelReservation(ctx, a.Agent.ID, a.Token); e != nil {
		t.Fatal(e)
	}
	if e := s.BeginLaunch(ctx, a.Agent.ID, a.Token, p); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("late launch %v", e)
	}
	if _, e := s.Claim(ctx, a.Agent.ID, a.Token, p, ecosystem.PaneInfo{}); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("late claim %v", e)
	}
	b := reserve(t, s, r, "starting")
	if e := s.BeginLaunch(ctx, b.Agent.ID, b.Token, p); e != nil {
		t.Fatal(e)
	}
	if e := s.CancelReservation(ctx, b.Agent.ID, b.Token); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("cancelled possible child: %v", e)
	}
	if e := s.FailLaunch(ctx, b.Agent.ID, b.Token, false); e == nil {
		t.Fatal("ambiguous launch released")
	}
	if stats(t, s, r.ID).Live != 2 {
		t.Fatal("ambiguous slot lost")
	}
	if _, e := s.Stop(ctx, r.ID, b.Agent.ID, false); e != nil {
		t.Fatal(e)
	}
	if stats(t, s, r.ID).Live != 2 {
		t.Fatal("stopping slot released")
	}
	if _, e := s.Claim(ctx, b.Agent.ID, b.Token, p, ecosystem.PaneInfo{}); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("stopping launch claimed: %v", e)
	}
	if e := s.FailLaunch(ctx, b.Agent.ID, b.Token, true); e != nil {
		t.Fatal(e)
	}
	c := reserve(t, s, r, "claimed")
	child := claim(t, s, c)
	if e := s.FailLaunch(ctx, child.ID, c.Token, true); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("claimed child rolled back: %v", e)
	}
	if e := s.PublishConclusion(ctx, child.ID, "verified result"); e != nil {
		t.Fatal(e)
	}
	if e := s.ConfirmStopped(ctx, child.ID, p, ""); e != nil {
		t.Fatal(e)
	}
	if a := get(t, s, child.ID); a.Conclusion != "verified result" || a.State != ecosystem.Completed {
		t.Fatalf("summary lost %+v", a)
	}
}

func TestObservedLaunchIdentityAndClaimRace(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	a := reserve(t, s, r, "observed")
	if e := s.BeginLaunch(ctx, a.Agent.ID, a.Token, identity(t)); e != nil {
		t.Fatal(e)
	}
	paneProcess := ecosystem.ProcessIdentity{PID: 123, StartToken: "exact-pane-incarnation"}
	pane := ecosystem.PaneInfo{SessionID: "$2", PaneID: "%4"}
	if e := s.ObserveLaunch(ctx, a.Agent.ID, "wrong", paneProcess, pane); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("wrong token accepted %v", e)
	}
	if e := s.ObserveLaunch(ctx, a.Agent.ID, a.Token, paneProcess, pane); e != nil {
		t.Fatal(e)
	}
	if got := get(t, s, a.Agent.ID); got.Launcher != paneProcess || got.Process.Valid() || got.Pane != pane {
		t.Fatalf("launch metadata %+v", got)
	}
	if e := s.FailLaunch(ctx, a.Agent.ID, a.Token, true); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("no-child assertion contradicted observed pane: %v", e)
	}
	if e := s.Reconcile(ctx, r.ID, func(p ecosystem.ProcessIdentity) (bool, error) { return p == paneProcess, nil }); e != nil {
		t.Fatal(e)
	}
	if get(t, s, a.Agent.ID).State != ecosystem.Starting {
		t.Fatal("live pane released when original launcher died")
	}
	if _, e := s.Claim(ctx, a.Agent.ID, a.Token, paneProcess, pane); e != nil {
		t.Fatal(e)
	}
	if e := s.ObserveLaunch(ctx, a.Agent.ID, a.Token, paneProcess, pane); e != nil {
		t.Fatalf("observation lost claim race %v", e)
	}
	wrong := paneProcess
	wrong.StartToken = "another-process"
	if e := s.ObserveLaunch(ctx, a.Agent.ID, a.Token, wrong, pane); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("claimed identity overwritten %v", e)
	}
}

func TestStopReparentsAndSubtreeIsAtomic(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 8)
	manager := claim(t, s, reserve(t, s, r, "manager"))
	if e := s.SetRole(ctx, manager.ID, ecosystem.Manager); e != nil {
		t.Fatal(e)
	}
	child := claim(t, s, reserve(t, s, manager, "child"))
	if _, e := s.Stop(ctx, child.ID, manager.ID, false); e != nil {
		t.Fatal(e)
	}
	if get(t, s, child.ID).ParentID != r.ID || get(t, s, child.ID).State != ecosystem.Running {
		t.Fatal("individual stop killed/orphaned child")
	}
	if get(t, s, manager.ID).State != ecosystem.Stopping {
		t.Fatal("manager not stopping")
	}
	if e := s.Heartbeat(ctx, manager.ID, identity(t), ecosystem.Running); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("heartbeat revived stopping %v", e)
	}
	other := root(t, s, 1)
	if _, e := s.Stop(ctx, other.ID, child.ID, false); e == nil {
		t.Fatal("cross ecosystem stop accepted")
	}
	pending := reserve(t, s, r, "pending")
	targets, e := s.Stop(ctx, child.ID, r.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(targets) != 4 {
		t.Fatalf("root stop targets %d", len(targets))
	}
	if get(t, s, pending.Agent.ID).State != ecosystem.Cancelled {
		t.Fatal("unlaunched reservation retained")
	}
	for _, id := range []string{r.ID, manager.ID, child.ID} {
		if get(t, s, id).State != ecosystem.Stopping {
			t.Fatalf("not stopping %s", id)
		}
	}
	if _, e = s.Reserve(ctx, r.ID, "late", ""); !errors.Is(e, ecosystem.ErrRole) {
		t.Fatalf("stopping root spawned %v", e)
	}
}

func TestReconcileUnknownIdentityAndLateClaim(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 3)
	p := identity(t)
	a := reserve(t, s, r, "startup")
	if e := s.BeginLaunch(ctx, a.Agent.ID, a.Token, p); e != nil {
		t.Fatal(e)
	}
	b := claim(t, s, reserve(t, s, r, "running"))
	unknown := errors.New("probe permission denied")
	if e := s.Reconcile(ctx, r.ID, func(ecosystem.ProcessIdentity) (bool, error) { return false, unknown }); !errors.Is(e, unknown) {
		t.Fatal(e)
	}
	if stats(t, s, r.ID).Live != 3 {
		t.Fatal("unknown process released")
	}
	// Heartbeat freshness is immaterial; only the exact process probe decides.
	if e := s.Reconcile(ctx, r.ID, func(ecosystem.ProcessIdentity) (bool, error) { return true, nil }); e != nil {
		t.Fatal(e)
	}
	if stats(t, s, r.ID).Live != 3 {
		t.Fatal("alive process released")
	}
	// Change a process during its probe to exercise compare-before-reclaim.
	count := 0
	if e := s.Reconcile(ctx, r.ID, func(x ecosystem.ProcessIdentity) (bool, error) {
		count++
		if count == 2 {
			_, e := s.Claim(ctx, a.Agent.ID, a.Token, ecosystem.ProcessIdentity{PID: 123, StartToken: "new-child"}, ecosystem.PaneInfo{})
			if e != nil {
				t.Fatal(e)
			}
		}
		return false, nil
	}); e != nil {
		t.Fatal(e)
	}
	if get(t, s, a.Agent.ID).State != ecosystem.Running {
		t.Fatal("stale launcher observation reclaimed claimed child")
	}
	if get(t, s, b.ID).State != ecosystem.Completed {
		t.Fatal("dead process retained")
	}
	// Reconcile only the remaining child as dead; then a late Claim is fenced.
	if e := s.Reconcile(ctx, r.ID, func(ecosystem.ProcessIdentity) (bool, error) { return false, nil }); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Claim(ctx, a.Agent.ID, a.Token, p, ecosystem.PaneInfo{}); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("late claim %v", e)
	}
}

func TestPersistentSummariesAndRootResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s := open(t, path)
	r := root(t, s, 2)
	child := claim(t, s, reserve(t, s, r, "worker"))
	if e := s.PublishConclusion(ctx, child.ID, "Tests pass; no context included."); e != nil {
		t.Fatal(e)
	}
	reopened := open(t, path)
	if got := get(t, reopened, child.ID); got.Conclusion == "" || got.Task != "task worker" {
		t.Fatalf("metadata lost %+v", got)
	}
	if _, e := s.ResumeRoot(ctx, r.ID, identity(t), ecosystem.PaneInfo{}, func(ecosystem.ProcessIdentity) (bool, error) { return true, nil }); e == nil {
		t.Fatal("resumed live root")
	}
	// Reclaim a dead root while its child remains live, then resume the same ID.
	calls := 0
	if e := s.Reconcile(ctx, r.ID, func(ecosystem.ProcessIdentity) (bool, error) { calls++; return calls != 1, nil }); e != nil {
		t.Fatal(e)
	}
	if e := s.SetMax(ctx, r.ID, 1); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ResumeRoot(ctx, r.ID, identity(t), ecosystem.PaneInfo{}, func(ecosystem.ProcessIdentity) (bool, error) { return false, nil }); !errors.Is(e, ecosystem.ErrCapacity) {
		t.Fatalf("resume exceeded cap %v", e)
	}
	if e := s.SetMax(ctx, r.ID, 2); e != nil {
		t.Fatal(e)
	}
	resumed, e := s.ResumeRoot(ctx, r.ID, identity(t), ecosystem.PaneInfo{PaneID: "%9"}, func(ecosystem.ProcessIdentity) (bool, error) { return false, nil })
	if e != nil {
		t.Fatal(e)
	}
	if resumed.ID != r.ID || resumed.Role != ecosystem.Manager || resumed.Pane.PaneID != "%9" {
		t.Fatalf("resume %+v", resumed)
	}
	if stats(t, reopened, r.ID).Live != 2 {
		t.Fatal("resume count")
	}
}

// Separate Store objects alone do not prove cross-process admission correctness.
// Helpers open the same file and race on its final live slots.
func TestProcessHelper(t *testing.T) {
	if os.Getenv("KH_ECO_TEST_HELPER") != "1" {
		return
	}
	s, e := ecosystem.Open(os.Getenv("KH_ECO_TEST_PATH"))
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	r, e := s.Reserve(ctx, os.Getenv("KH_ECO_TEST_ROOT"), os.Getenv("KH_ECO_TEST_NAME"), "helper task")
	if errors.Is(e, ecosystem.ErrCapacity) {
		fmt.Print("capacity")
		s.Close()
		os.Exit(0)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		s.Close()
		os.Exit(2)
	}
	json.NewEncoder(os.Stdout).Encode(r)
	s.Close()
	os.Exit(0)
}
func helper(path, root, name string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	cmd.Env = append(os.Environ(), "KH_ECO_TEST_HELPER=1", "KH_ECO_TEST_PATH="+path, "KH_ECO_TEST_ROOT="+root, "KH_ECO_TEST_NAME="+name)
	return cmd
}
func TestCrossProcessAtomicAdmissionAndOrphanReservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s := open(t, path)
	r := root(t, s, 4)
	const competitors = 12
	var wg sync.WaitGroup
	results := make(chan string, competitors)
	failures := make(chan error, competitors)
	for i := 0; i < competitors; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, e := helper(path, r.ID, fmt.Sprintf("worker-%d", i)).CombinedOutput()
			if e != nil {
				failures <- fmt.Errorf("%w: %s", e, out)
				return
			}
			results <- strings.TrimSpace(string(out))
		}(i)
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	admitted, capped := 0, 0
	var reservation ecosystem.Reservation
	for out := range results {
		if out == "capacity" {
			capped++
			continue
		}
		if e := json.Unmarshal([]byte(out), &reservation); e != nil {
			t.Fatalf("helper output %q: %v", out, e)
		}
		admitted++
	}
	if admitted != 3 || capped != 9 {
		t.Fatalf("admitted=%d capacity=%d", admitted, capped)
	}
	if stats(t, s, r.ID).Live != 4 {
		t.Fatal("count exceeds shared cap")
	}
	// The spawning utility processes died BEFORE BeginLaunch. Their manager
	// remains alive: reservation ownership must not be confused with parenthood.
	if alive, e := ecosystem.ProbeProcess(r.Process); e != nil || !alive {
		t.Fatalf("root not alive %v %v", alive, e)
	}
	if e := s.Reconcile(ctx, r.ID, ecosystem.ProbeProcess); e != nil {
		t.Fatal(e)
	}
	if stats(t, s, r.ID).Live != 1 {
		t.Fatal("orphan pre-launch reservations leaked")
	}
	if e := s.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, identity(t)); !errors.Is(e, ecosystem.ErrState) {
		t.Fatalf("cancelled reservation launched %v", e)
	}
	reserve(t, s, r, "replacement")
}

func TestProcessIdentityRejectsPIDReuse(t *testing.T) {
	p := identity(t)
	again, e := ecosystem.ProcessForPID(os.Getpid())
	if e != nil || again != p {
		t.Fatalf("unstable identity %+v %+v %v", p, again, e)
	}
	if alive, e := ecosystem.ProbeProcess(p); e != nil || !alive {
		t.Fatalf("current process %v %v", alive, e)
	}
	stale := p
	stale.StartToken += "-old"
	if alive, e := ecosystem.ProbeProcess(stale); e != nil || alive {
		t.Fatalf("reused PID accepted %v %v", alive, e)
	}
	if _, e := ecosystem.ProbeProcess(ecosystem.ProcessIdentity{PID: os.Getpid()}); !errors.Is(e, ecosystem.ErrIdentity) {
		t.Fatalf("PID-only probe %v", e)
	}
}
