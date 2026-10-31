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

func descendants(t *testing.T, s *ecosystem.Store, id string, want int, parent bool) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	count, gotParent, err := s.LiveDescendants(readCtx, id)
	if err != nil || count != want || gotParent != parent {
		t.Fatalf("LiveDescendants(%s) = (%d, %v, %v), want (%d, %v, nil)", id, count, gotParent, err, want, parent)
	}
}

func TestLiveDescendantsStates(t *testing.T) {
	for _, state := range []ecosystem.State{ecosystem.Reserved, ecosystem.Starting, ecosystem.Running, ecosystem.Idle, ecosystem.Stopping, ecosystem.Completed, ecosystem.Failed, ecosystem.Cancelled} {
		t.Run(string(state), func(t *testing.T) {
			s := open(t, filepath.Join(t.TempDir(), "registry.db"))
			r := root(t, s, 3)
			descendants(t, s, r.ID, 0, true) // Root displays (0), but never counts itself.
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
			descendants(t, s, r.ID, want, true)
			descendants(t, s, reservation.Agent.ID, 0, false)
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
	descendants(t, s, manager.ID, 0, true)
	leaf := claim(t, s, reserve(t, s, manager, "leaf"))
	reserve(t, s, r, "sibling")
	// A separate root in the same registry must never contribute to this tree.
	unrelated := root(t, s, 3)
	reserve(t, s, unrelated, "unrelated")
	descendants(t, s, r.ID, 3, true)
	descendants(t, s, manager.ID, 1, true)
	descendants(t, s, leaf.ID, 0, false)
	if err := s.SetRole(ctx, manager.ID, ecosystem.Worker); err != nil {
		t.Fatal(err)
	}
	descendants(t, s, manager.ID, 1, true)
	if err := s.ConfirmStopped(ctx, manager.ID, manager.Process, "completed ancestor"); err != nil {
		t.Fatal(err)
	}
	descendants(t, s, r.ID, 2, true) // Traverse completed ancestors to their live children.
	descendants(t, s, manager.ID, 1, true)
	if err := s.ConfirmStopped(ctx, leaf.ID, leaf.Process, "completed leaf"); err != nil {
		t.Fatal(err)
	}
	descendants(t, s, manager.ID, 0, true) // Keep a parent's visible (0) after completion.
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
			descendants(t, s, r.ID, 0, true)
			if kind == "cross-ecosystem" {
				descendants(t, s, parent, 0, true) // Foreign parent IDs cannot import this subtree.
			}
		})
	}
}
