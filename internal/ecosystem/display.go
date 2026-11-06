package ecosystem

import (
	"context"
	"database/sql"
)

// DisplayResult is an explicitly captured assignment and its conclusion.
// Task is nil for historical or uncaptured assignments. This is a display-only
// API; the safe result feed intentionally does not include assignment text.
type DisplayResult struct {
	Result
	Task *string
}

func validateDisplayTask(task []string) error {
	if len(task) > 1 || (len(task) == 1 && len(task[0]) > 64*1024) {
		return ErrState
	}
	return nil
}
func saveDisplayTask(ctx context.Context, c *sql.Conn, id, kind, taskID string, generation int64, task []string) error {
	if len(task) == 0 {
		return nil
	}
	_, err := c.ExecContext(ctx, `INSERT INTO agent_display_tasks(agent_id,kind,task_id,generation,task) VALUES(?,?,?,?,?)`, id, kind, taskID, generation, task[0])
	return err
}
func (s *Store) rollbackLaunchDisplay(ctx context.Context, id, token, query string) error {
	return s.write(ctx, func(c *sql.Conn) error {
		if err := changed(c.ExecContext(ctx, query, id, token)); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, `DELETE FROM agent_display_tasks WHERE agent_id=? AND kind='launch' AND result_seq IS NULL`, id)
		return err
	})
}

// DisplayResultsAfter has the same parent scope, ordering and page bounds as
// ResultsAfter. Pairing is explicitly bound at publication, never inferred from
// the worker's mutable current assignment.
func (s *Store) DisplayResultsAfter(ctx context.Context, parentID string, after int64, limit int) ([]DisplayResult, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.seq,r.agent_id,r.name,r.task_id,r.generation,r.conclusion,r.truncated,d.task FROM agent_results r JOIN agents p ON p.id=r.parent_id AND p.ecosystem_id=r.ecosystem_id LEFT JOIN agent_display_tasks d ON d.result_seq=r.seq WHERE p.id=? AND r.seq>? ORDER BY r.seq LIMIT ?`, parentID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DisplayResult{}
	for rows.Next() {
		var r DisplayResult
		if err = rows.Scan(&r.Seq, &r.AgentID, &r.Name, &r.TaskID, &r.Generation, &r.Conclusion, &r.Truncated, &r.Task); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
