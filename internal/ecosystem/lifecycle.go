package ecosystem

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ProcessProbe returns false only when this exact process incarnation is proven
// absent. Permission errors, unsupported platforms and ambiguous observations
// must return an error, not false. PID-only liveness checks are insufficient.
type ProcessProbe func(ProcessIdentity) (bool, error)

// Stop atomically marks targets before the caller signals any processes. Every
// live participant may stop another participant in its own ecosystem. Stopping
// the root always means the whole ecosystem. Individual stops reparent surviving
// direct children to the stopped agent's parent.
func (s *Store) Stop(ctx context.Context, actorID, targetID string, subtree bool) ([]Agent, error) {
	var targets []Agent
	err := s.write(ctx, func(c *sql.Conn) error {
		actor, err := get(ctx, c, actorID)
		if err != nil {
			return err
		}
		if !actor.State.Live() {
			return ErrState
		}
		target, err := get(ctx, c, targetID)
		if err != nil {
			return err
		}
		if actor.EcosystemID != target.EcosystemID {
			return errors.New("target belongs to another ecosystem")
		}
		rows, err := c.QueryContext(ctx, "SELECT "+columns+" FROM agents WHERE ecosystem_id=?", target.EcosystemID)
		if err != nil {
			return err
		}
		var all []Agent
		for rows.Next() {
			a, e := scan(rows)
			if e != nil {
				rows.Close()
				return e
			}
			all = append(all, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		selected := map[string]bool{target.ID: true}
		if subtree || target.ParentID == "" {
			for changed := true; changed; {
				changed = false
				for _, a := range all {
					if selected[a.ParentID] && !selected[a.ID] {
						selected[a.ID] = true
						changed = true
					}
				}
			}
		}
		for _, a := range all {
			if !selected[a.ID] || !a.State.Live() {
				continue
			}
			state := Stopping
			if a.State == Reserved {
				state = Cancelled
			}
			if _, err = c.ExecContext(ctx, "UPDATE agents SET state=? WHERE id=?", state, a.ID); err != nil {
				return err
			}
			a.State = state
			targets = append(targets, a)
		}
		if !subtree && target.ParentID != "" {
			_, err = c.ExecContext(ctx, "UPDATE agents SET parent_id=? WHERE parent_id=? AND state IN ('reserved','starting','running','idle','stopping')", target.ParentID, target.ID)
		}
		return err
	})
	return targets, err
}

// ConfirmStopped is for a caller that has proof of process exit (for example
// Wait on an owned child). Merely sending a signal is NOT proof of exit. Use
// Reconcile with an exact identity probe when the caller does not own Wait.
func (s *Store) ConfirmStopped(ctx context.Context, id string, p ProcessIdentity, conclusion string) error {
	if !p.Valid() {
		return ErrIdentity
	}
	return changed(s.db.ExecContext(ctx, `UPDATE agents SET state='completed',conclusion=CASE WHEN ?='' THEN conclusion ELSE ? END WHERE id=? AND pid=? AND start_token=? AND state IN ('running','idle','stopping')`, conclusion, conclusion, id, p.PID, p.StartToken))
}

// Reconcile reclaims only proven-dead process incarnations. Heartbeat age is
// diagnostic, never evidence of exit. An unclaimed startup is cancelled only if
// its launcher has died: Claim then rejects a late child before it can do work.
// Reserve records its actual caller's identity, covering crashes even before
// BeginLaunch (including utility callers distinct from the manager process).
func (s *Store) Reconcile(ctx context.Context, ecoID string, probe ProcessProbe) error {
	if probe == nil {
		return errors.New("process probe required")
	}
	// Only live identities are needed for probing. Do not load potentially large
	// task/conclusion payloads or completed history on periodic refreshes.
	rows, err := s.db.QueryContext(ctx, `SELECT id,pid,start_token,launcher_pid,launcher_token FROM agents WHERE ecosystem_id=? AND state IN ('reserved','starting','running','idle','stopping')`, ecoID)
	if err != nil {
		return err
	}
	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Process.PID, &a.Process.StartToken, &a.Launcher.PID, &a.Launcher.StartToken); err != nil {
			rows.Close()
			return err
		}
		agents = append(agents, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, a := range agents {
		p := a.Process
		if !p.Valid() {
			p = a.Launcher
		}
		if !p.Valid() {
			continue
		}
		alive, e := probe(p)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		if alive {
			continue
		}
		e = s.write(ctx, func(c *sql.Conn) error {
			current, err := get(ctx, c, a.ID)
			if err != nil {
				return err
			}
			// Another process may have claimed, resumed, or otherwise changed the row
			// while we probed. Never apply an observation to a new incarnation.
			if current.Process != a.Process || current.Launcher != a.Launcher || !current.State.Live() {
				return nil
			}
			state := Completed
			if !current.Process.Valid() {
				state = Cancelled
			}
			_, err = c.ExecContext(ctx, "UPDATE agents SET state=? WHERE id=?", state, a.ID)
			return err
		})
		if e != nil {
			failures = append(failures, e)
		}
	}
	return errors.Join(failures...)
}

// ResumeRoot reuses a durable root after verifying its previous incarnation is
// absent. It cannot double-admit a live saved session. Capacity is checked again
// because descendants may still be running while their root is absent.
func (s *Store) ResumeRoot(ctx context.Context, id string, p ProcessIdentity, pane PaneInfo, probe ProcessProbe) (Agent, error) {
	if !p.Valid() || probe == nil {
		return Agent{}, ErrIdentity
	}
	var result Agent
	err := s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, id)
		if err != nil {
			return err
		}
		if a.ParentID != "" {
			return errors.New("only a root can resume")
		}
		alive, err := probe(a.Process)
		if err != nil {
			return err
		}
		if alive {
			return errors.New("saved ecosystem root is still running")
		}
		if !a.State.Live() {
			var max, live int
			if err = c.QueryRowContext(ctx, "SELECT max_agents FROM ecosystems WHERE id=?", a.EcosystemID).Scan(&max); err != nil {
				return err
			}
			if err = c.QueryRowContext(ctx, "SELECT count(*) FROM agents WHERE ecosystem_id=? AND state IN ('reserved','starting','running','idle','stopping')", a.EcosystemID).Scan(&live); err != nil {
				return err
			}
			if live >= max {
				return ErrCapacity
			}
		}
		_, err = c.ExecContext(ctx, "UPDATE agents SET state='running',role='manager',pid=?,start_token=?,session_id=?,pane_id=?,heartbeat=? WHERE id=?", p.PID, p.StartToken, pane.SessionID, pane.PaneID, time.Now().UnixNano(), id)
		if err != nil {
			return err
		}
		result, err = get(ctx, c, id)
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	return result, nil
}
