package ecosystem

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"
)

// Result is a bounded conclusion, never an agent conversation or task prompt.
type Result struct {
	Seq        int64  `json:"seq"`
	AgentID    string `json:"agent_id"`
	Name       string `json:"name"`
	TaskID     string `json:"task_id"`
	Generation int    `json:"generation"`
	Conclusion string `json:"conclusion"`
	Truncated  bool   `json:"truncated"`
}

func (s *Store) initResults() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_results (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 ecosystem_id TEXT NOT NULL, parent_id TEXT NOT NULL,
 agent_id TEXT NOT NULL, name TEXT NOT NULL, task_id TEXT NOT NULL,
 generation INTEGER NOT NULL, conclusion TEXT NOT NULL, truncated INTEGER NOT NULL,
 UNIQUE(agent_id,task_id,generation));
 CREATE INDEX IF NOT EXISTS agent_results_parent ON agent_results(ecosystem_id,parent_id,seq);`)
	return err
}

// boundConclusion applies the single byte budget without splitting UTF-8.
func boundConclusion(text string) (string, bool) {
	truncated := len(text) > 16000
	text = strings.ToValidUTF8(text, "")
	if truncated {
		if len(text) > 15997 {
			text = text[:15997]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
		}
		text += "…"
	}
	return text, truncated
}

func appendResult(ctx context.Context, c *sql.Conn, a Agent, taskID string, generation int64, conclusion string, truncated bool) error {
	if a.ParentID == "" {
		return nil
	}
	_, err := c.ExecContext(ctx, `INSERT INTO agent_results(ecosystem_id,parent_id,agent_id,name,task_id,generation,conclusion,truncated) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(agent_id,task_id,generation) DO NOTHING`, a.EcosystemID, a.ParentID, a.ID, a.Name, taskID, generation, conclusion, truncated)
	return err
}

// PublishResult publishes one ordinary descendant turn. Retrying the same
// publication ID is idempotent; separate turns may have identical conclusions.
func (s *Store) PublishResult(ctx context.Context, agentID, publicationID, conclusion string) error {
	if publicationID == "" || len(publicationID) > 128 {
		return ErrState
	}
	for _, r := range publicationID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_:.", r)) {
			return ErrState
		}
	}
	conclusion, truncated := boundConclusion(conclusion)
	return s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, agentID)
		if err != nil {
			return err
		}
		if a.ParentID == "" {
			return nil
		}
		if err = appendResult(ctx, c, a, publicationID, 1, conclusion, truncated); err != nil {
			return err
		}
		// A duplicate publication must not overwrite the inspect conclusion.
		var inserted int
		if err = c.QueryRowContext(ctx, `SELECT changes()`).Scan(&inserted); err != nil {
			return err
		}
		if inserted == 0 {
			return nil
		}
		return changed(c.ExecContext(ctx, `UPDATE agents SET conclusion=? WHERE id=?`, conclusion, agentID))
	})
}

// ResultWatermark returns the newest sequence visible to this direct parent.
func (s *Store) ResultWatermark(ctx context.Context, parentID string) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(r.seq),0) FROM agent_results r JOIN agents p ON p.id=r.parent_id AND p.ecosystem_id=r.ecosystem_id WHERE p.id=?`, parentID).Scan(&seq)
	return seq, err
}

// ResultsAfter returns ascending, parent/ecosystem-scoped pages. The default
// page is 100 rows and no page may exceed 1000 rows.
func (s *Store) ResultsAfter(ctx context.Context, parentID string, after int64, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.seq,r.agent_id,r.name,r.task_id,r.generation,r.conclusion,r.truncated FROM agent_results r JOIN agents p ON p.id=r.parent_id AND p.ecosystem_id=r.ecosystem_id WHERE p.id=? AND r.seq>? ORDER BY r.seq LIMIT ?`, parentID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var r Result
		if err = rows.Scan(&r.Seq, &r.AgentID, &r.Name, &r.TaskID, &r.Generation, &r.Conclusion, &r.Truncated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
