package ecosystem_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"kh/internal/ecosystem"
)

func descendants(t *testing.T, s *ecosystem.Store, id string, want int, role ecosystem.Role) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	count, gotRole, err := s.LiveDescendants(readCtx, id)
	if err != nil || count != want || gotRole != role {
		t.Fatalf("LiveDescendants(%s) = (%d, %v, %v), want (%d, %v, nil)", id, count, gotRole, err, want, role)
	}
}

func TestLiveDescendantsStates(t *testing.T) {
	for _, state := range []ecosystem.State{ecosystem.Reserved, ecosystem.Starting, ecosystem.Running, ecosystem.Idle, ecosystem.Stopping, ecosystem.Completed, ecosystem.Failed, ecosystem.Cancelled} {
		t.Run(string(state), func(t *testing.T) {
			s := open(t, filepath.Join(t.TempDir(), "registry.db"))
			r := root(t, s, 3)
			descendants(t, s, r.ID, 0, ecosystem.Manager) // Never count self.
			reservation := reserve(t, s, r, "child")
			switch state {
			case ecosystem.Reserved:
			case ecosystem.Starting:
				if err := s.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, identity(t)); err != nil {
					t.Fatal(err)
				}
			case ecosystem.Failed:
				if err := s.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, identity(t)); err != nil {
					t.Fatal(err)
				}
				if err := s.FailLaunch(ctx, reservation.Agent.ID, reservation.Token, true); err != nil {
					t.Fatal(err)
				}
			case ecosystem.Cancelled:
				if err := s.CancelReservation(ctx, reservation.Agent.ID, reservation.Token); err != nil {
					t.Fatal(err)
				}
			default:
				child := claim(t, s, reservation)
				switch state {
				case ecosystem.Idle:
					if err := s.Heartbeat(ctx, child.ID, child.Process, ecosystem.Idle); err != nil {
						t.Fatal(err)
					}
				case ecosystem.Stopping:
					if _, err := s.Stop(ctx, r.ID, child.ID, true); err != nil {
						t.Fatal(err)
					}
				case ecosystem.Completed:
					if err := s.ConfirmStopped(ctx, child.ID, child.Process, "fixture completed"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := get(t, s, reservation.Agent.ID).State; got != state {
				t.Fatalf("fixture state %s, want %s", got, state)
			}
			want := 0
			if state.Live() {
				want = 1
			}
			descendants(t, s, r.ID, want, ecosystem.Manager)
			descendants(t, s, reservation.Agent.ID, 0, ecosystem.Worker)
		})
	}
}

func TestLiveDescendantsNestedDemotedAndCompletedParents(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"))
	r := root(t, s, 10)
	manager := claim(t, s, reserve(t, s, r, "manager"))
	if err := s.SetRole(ctx, manager.ID, ecosystem.Manager); err != nil {
		t.Fatal(err)
	}
	descendants(t, s, manager.ID, 0, ecosystem.Manager)
	leaf := claim(t, s, reserve(t, s, manager, "leaf"))
	reserve(t, s, r, "sibling")
	// A separate root in the same registry must never contribute to this tree.
	unrelated := root(t, s, 3)
	reserve(t, s, unrelated, "unrelated")
	descendants(t, s, r.ID, 3, ecosystem.Manager)
	descendants(t, s, manager.ID, 1, ecosystem.Manager)
	descendants(t, s, leaf.ID, 0, ecosystem.Worker)
	if err := s.SetRole(ctx, manager.ID, ecosystem.Worker); err != nil {
		t.Fatal(err)
	}
	descendants(t, s, manager.ID, 1, ecosystem.Worker)
	if err := s.ConfirmStopped(ctx, manager.ID, manager.Process, "completed ancestor"); err != nil {
		t.Fatal(err)
	}
	descendants(t, s, r.ID, 2, ecosystem.Manager) // Traverse completed ancestors to their live children.
	descendants(t, s, manager.ID, 1, ecosystem.Worker)
	if err := s.ConfirmStopped(ctx, leaf.ID, leaf.Process, "completed leaf"); err != nil {
		t.Fatal(err)
	}
	descendants(t, s, manager.ID, 0, ecosystem.Worker) // Keep the role after completion.
	if _, _, err := s.LiveDescendants(ctx, "missing"); !errors.Is(err, ecosystem.ErrNotFound) {
		t.Fatalf("missing agent: %v", err)
	}
}

func TestLiveDescendantsRejectsInvalidAncestry(t *testing.T) {
	// Corrupt only a private fixture database to exercise defensive ancestry
	// traversal through the public reader, without production test-only APIs.
	for _, kind := range []string{"orphan", "cycle", "cross-ecosystem"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registry.db")
			s := open(t, path)
			r := root(t, s, 5)
			branch := claim(t, s, reserve(t, s, r, "branch"))
			if err := s.SetRole(ctx, branch.ID, ecosystem.Manager); err != nil {
				t.Fatal(err)
			}
			leaf := reserve(t, s, branch, "leaf")
			parent := "missing"
			if kind == "cycle" {
				parent = leaf.Agent.ID
			}
			if kind == "cross-ecosystem" {
				parent = root(t, s, 2).ID
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec("UPDATE agents SET parent_id=? WHERE id=?", parent, branch.ID); err != nil {
				t.Fatal(err)
			}
			descendants(t, s, r.ID, 0, ecosystem.Manager)
			if kind == "cross-ecosystem" {
				descendants(t, s, parent, 0, ecosystem.Manager) // Foreign parent IDs cannot import this subtree.
			}
		})
	}
}
