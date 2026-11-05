package ecosystem

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// TaskLease fences assignments independently of process heartbeats.
type TaskLease struct {
	ID         string `json:"id"`
	Generation int64  `json:"generation"`
}
type TaskTimings struct {
	StartupMS   int64 `json:"startup_ms"`
	ContextMS   int64 `json:"context_ms"`
	ExecutionMS int64 `json:"execution_ms"`
	ReportMS    int64 `json:"report_ms"`
}
type TaskStatus struct {
	Lease    TaskLease   `json:"lease"`
	Status   string      `json:"status"`
	Timings  TaskTimings `json:"timings_ms"`
	Reusable bool        `json:"reusable"`
}

func (s *Store) initTasks() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_tasks(
 agent_id TEXT PRIMARY KEY REFERENCES agents(id), reusable INTEGER NOT NULL DEFAULT 0,
 lease_id TEXT NOT NULL DEFAULT '', generation INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT '' CHECK(status IN ('','pending','running','completed')),
 startup_ms INTEGER NOT NULL DEFAULT 0, context_ms INTEGER NOT NULL DEFAULT 0,
 execution_ms INTEGER NOT NULL DEFAULT 0, report_ms INTEGER NOT NULL DEFAULT 0);`)
	return err
}

func taskStatus(ctx context.Context, c *sql.Conn, id string) (TaskStatus, error) {
	var t TaskStatus
	err := c.QueryRowContext(ctx, `SELECT lease_id,generation,status,startup_ms,context_ms,execution_ms,report_ms,reusable FROM agent_tasks WHERE agent_id=?`, id).Scan(&t.Lease.ID, &t.Lease.Generation, &t.Status, &t.Timings.StartupMS, &t.Timings.ContextMS, &t.Timings.ExecutionMS, &t.Timings.ReportMS, &t.Reusable)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskStatus{}, nil
	}
	return t, err
}
func (s *Store) TaskStatus(ctx context.Context, id string) (TaskStatus, error) {
	var t TaskStatus
	err := s.write(ctx, func(c *sql.Conn) error {
		if _, err := get(ctx, c, id); err != nil {
			return err
		}
		var err error
		t, err = taskStatus(ctx, c, id)
		return err
	})
	return t, err
}
func taskOwner(a Agent, p ProcessIdentity) error {
	if !p.Valid() || a.Process != p {
		return ErrIdentity
	}
	if a.State != Running && a.State != Idle {
		return ErrState
	}
	return nil
}
func taskDescendants(ctx context.Context, c *sql.Conn, id string) (int, error) {
	var count int
	err := c.QueryRowContext(ctx, `WITH RECURSIVE tree(id,ecosystem_id) AS (
 SELECT id,ecosystem_id FROM agents WHERE id=?
 UNION SELECT a.id,a.ecosystem_id FROM agents a JOIN tree t ON a.parent_id=t.id AND a.ecosystem_id=t.ecosystem_id
 ) SELECT count(*) FROM agents a JOIN tree t ON a.id=t.id WHERE a.id!=? AND a.state IN ('reserved','starting','running','idle','stopping')`, id, id).Scan(&count)
	return count, err
}
func (s *Store) EnableReuse(ctx context.Context, id string, p ProcessIdentity) error {
	return s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, id)
		if err != nil {
			return err
		}
		if err = taskOwner(a, p); err != nil {
			return err
		}
		if a.ParentID == "" || a.Role != Worker {
			return ErrState
		}
		_, err = c.ExecContext(ctx, `INSERT INTO agent_tasks(agent_id,reusable) VALUES(?,1) ON CONFLICT(agent_id) DO UPDATE SET reusable=1`, id)
		return err
	})
}
func (s *Store) BeginInitialTask(ctx context.Context, id string, p ProcessIdentity, summary string) (TaskLease, error) {
	var lease TaskLease
	err := s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, id)
		if err != nil {
			return err
		}
		if err = taskOwner(a, p); err != nil {
			return err
		}
		if a.ParentID == "" {
			return ErrState
		}
		t, err := taskStatus(ctx, c, id)
		if err != nil {
			return err
		}
		if t.Lease.Generation != 0 {
			return ErrState
		}
		lease = TaskLease{randomID(), 1}
		_, err = c.ExecContext(ctx, `INSERT INTO agent_tasks(agent_id,lease_id,generation,status) VALUES(?,?,1,'running') ON CONFLICT(agent_id) DO UPDATE SET lease_id=excluded.lease_id,generation=1,status='running'`, id, lease.ID)
		if err != nil {
			return err
		}
		return changed(c.ExecContext(ctx, `UPDATE agents SET task=?,conclusion='',state='running' WHERE id=?`, taskSummary(summary), id))
	})
	if err != nil {
		return TaskLease{}, err
	}
	return lease, nil
}
func (s *Store) ReserveTask(ctx context.Context, actorID, targetID, summary string) (TaskLease, error) {
	var lease TaskLease
	err := s.write(ctx, func(c *sql.Conn) error {
		actor, err := get(ctx, c, actorID)
		if err != nil {
			return err
		}
		if actor.Role != Manager || (actor.State != Running && actor.State != Idle) {
			return ErrRole
		}
		a, err := get(ctx, c, targetID)
		if err != nil {
			return err
		}
		if a.ParentID != actor.ID || a.EcosystemID != actor.EcosystemID || a.Role != Worker || a.State != Idle || !a.Process.Valid() {
			return ErrState
		}
		t, err := taskStatus(ctx, c, targetID)
		if err != nil {
			return err
		}
		if !t.Reusable || t.Status == "pending" || t.Status == "running" {
			return ErrState
		}
		n, err := taskDescendants(ctx, c, targetID)
		if err != nil {
			return err
		}
		if n != 0 {
			return ErrState
		}
		lease = TaskLease{randomID(), t.Lease.Generation + 1}
		if err = changed(c.ExecContext(ctx, `UPDATE agent_tasks SET lease_id=?,generation=?,status='pending',startup_ms=0,context_ms=0,execution_ms=0,report_ms=0 WHERE agent_id=?`, lease.ID, lease.Generation, targetID)); err != nil {
			return err
		}
		return changed(c.ExecContext(ctx, `UPDATE agents SET task=?,conclusion='',state='running' WHERE id=?`, taskSummary(summary), targetID))
	})
	if err != nil {
		return TaskLease{}, err
	}
	return lease, nil
}

// CancelTaskReservation rolls back only a still-pending exact lease. A frame
// already consumed by ClaimTask wins the race and cannot be released here.
func (s *Store) CancelTaskReservation(ctx context.Context, actorID, targetID string, p ProcessIdentity, l TaskLease) error {
	return s.write(ctx, func(c *sql.Conn) error {
		actor, err := get(ctx, c, actorID)
		if err != nil {
			return err
		}
		target, err := get(ctx, c, targetID)
		if err != nil {
			return err
		}
		if actor.Role != Manager || !actor.State.Live() || actor.EcosystemID != target.EcosystemID || target.ParentID != actorID {
			return ErrRole
		}
		if err := taskOwner(target, p); err != nil {
			return err
		}
		if l.ID == "" || l.Generation < 1 {
			return ErrState
		}
		if err := changed(c.ExecContext(ctx, `UPDATE agent_tasks SET status='completed' WHERE agent_id=? AND lease_id=? AND generation=? AND status='pending'`, targetID, l.ID, l.Generation)); err != nil {
			return err
		}
		return changed(c.ExecContext(ctx, `UPDATE agents SET state='idle',conclusion='Task assignment cancelled before acceptance' WHERE id=?`, targetID))
	})
}

func (s *Store) ClaimTask(ctx context.Context, id string, p ProcessIdentity, l TaskLease) error {
	return s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, id)
		if err != nil {
			return err
		}
		if err = taskOwner(a, p); err != nil {
			return err
		}
		if l.ID == "" || l.Generation < 1 {
			return ErrState
		}
		return changed(c.ExecContext(ctx, `UPDATE agent_tasks SET status='running' WHERE agent_id=? AND lease_id=? AND generation=? AND status='pending'`, id, l.ID, l.Generation))
	})
}
func (s *Store) FinishTask(ctx context.Context, id string, p ProcessIdentity, l TaskLease, conclusion string, t TaskTimings) error {
	if len(conclusion) > 16000 {
		conclusion = strings.ToValidUTF8(conclusion[:16000], "") + "…"
	}
	if t.StartupMS < 0 || t.ContextMS < 0 || t.ExecutionMS < 0 || t.ReportMS < 0 {
		return errors.New("negative task timing")
	}
	return s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, id)
		if err != nil {
			return err
		}
		if err = taskOwner(a, p); err != nil {
			return err
		}
		if l.ID == "" || l.Generation < 1 {
			return ErrState
		}
		if err = changed(c.ExecContext(ctx, `UPDATE agent_tasks SET status='completed',startup_ms=?,context_ms=?,execution_ms=?,report_ms=? WHERE agent_id=? AND lease_id=? AND generation=? AND status='running'`, t.StartupMS, t.ContextMS, t.ExecutionMS, t.ReportMS, id, l.ID, l.Generation)); err != nil {
			return err
		}
		return changed(c.ExecContext(ctx, `UPDATE agents SET state='idle',conclusion=?,heartbeat=? WHERE id=?`, conclusion, time.Now().UnixNano(), id))
	})
}

// ExpireIdle is the retirement CAS, not proof of exit. The caller decides when
// its configured idle deadline has elapsed; stopping still occupies capacity.
func (s *Store) ExpireIdle(ctx context.Context, id string, p ProcessIdentity) error {
	return s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, id)
		if err != nil {
			return err
		}
		if err = taskOwner(a, p); err != nil {
			return err
		}
		if a.State != Idle || a.Role != Worker || a.ParentID == "" {
			return ErrState
		}
		t, err := taskStatus(ctx, c, id)
		if err != nil {
			return err
		}
		if !t.Reusable || t.Status == "pending" || t.Status == "running" {
			return ErrState
		}
		n, err := taskDescendants(ctx, c, id)
		if err != nil {
			return err
		}
		if n != 0 {
			return ErrState
		}
		return changed(c.ExecContext(ctx, `UPDATE agents SET state='stopping' WHERE id=? AND state='idle'`, id))
	})
}
