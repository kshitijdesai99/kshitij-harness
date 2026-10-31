// Package ecosystem stores coordination metadata, never conversation contexts.
// Its SQLite transactions are the admission authority shared by all processes.
package ecosystem

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const DefaultMaxAgents = 50

type Role string

const (
	Manager Role = "manager"
	Worker  Role = "worker"
)

type State string

const (
	Reserved  State = "reserved"
	Starting  State = "starting"
	Running   State = "running"
	Idle      State = "idle"
	Stopping  State = "stopping"
	Completed State = "completed"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

func (s State) Live() bool {
	return s == Reserved || s == Starting || s == Running || s == Idle || s == Stopping
}

type ProcessIdentity struct {
	PID        int    `json:"pid"`
	StartToken string `json:"start_token"`
}

func (p ProcessIdentity) Valid() bool { return p.PID > 0 && p.StartToken != "" }

type PaneInfo struct {
	SessionID string `json:"session_id"`
	PaneID    string `json:"pane_id"`
}
type Agent struct {
	ID          string          `json:"id"`
	EcosystemID string          `json:"ecosystem_id"`
	ParentID    string          `json:"parent_id,omitempty"`
	Name        string          `json:"name"`
	Address     string          `json:"address"`
	Role        Role            `json:"role"`
	State       State           `json:"state"`
	Task        string          `json:"task_summary,omitempty"`
	Conclusion  string          `json:"conclusion,omitempty"`
	Process     ProcessIdentity `json:"process"`
	Launcher    ProcessIdentity `json:"launcher"`
	Pane        PaneInfo        `json:"pane"`
	Heartbeat   time.Time       `json:"heartbeat"`
	Created     time.Time       `json:"created"`
}
type RootOptions struct {
	Name, Task string
	MaxAgents  int
	Process    ProcessIdentity
	Pane       PaneInfo
}
type Reservation struct {
	Agent Agent
	Token string
}
type Stats struct {
	MaxAgents int           `json:"max_agents"`
	Live      int           `json:"live"`
	Available int           `json:"available"`
	Managers  int           `json:"managers"`
	Workers   int           `json:"workers"`
	States    map[State]int `json:"states"`
}

var (
	ErrCapacity = errors.New("ecosystem live agent capacity reached")
	ErrNotFound = errors.New("ecosystem agent not found")
	ErrState    = errors.New("ecosystem invalid lifecycle transition")
	ErrRole     = errors.New("ecosystem only a live manager can spawn")
	ErrIdentity = errors.New("ecosystem process identity mismatch or unavailable")
)

type Store struct{ db *sql.DB }

// Open opens a shared registry. The parent directory is private when created.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("ecosystem registry path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	// SQLite derives new sidecar permissions from the main database file.
	// Secure all existing files before its first I/O, not after schema setup.
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err = os.Chmod(abs+suffix, 0600); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	u := url.URL{Scheme: "file", Path: abs}
	db, err := sql.Open("sqlite", u.String()+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	_, err = db.Exec(`PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS ecosystems(id TEXT PRIMARY KEY, max_agents INTEGER NOT NULL CHECK(max_agents>0));
 CREATE TABLE IF NOT EXISTS agents(
 id TEXT PRIMARY KEY, ecosystem_id TEXT NOT NULL REFERENCES ecosystems(id), parent_id TEXT NOT NULL,
 name TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('manager','worker')),
 state TEXT NOT NULL CHECK(state IN ('reserved','starting','running','idle','stopping','completed','failed','cancelled')),
 task TEXT NOT NULL, conclusion TEXT NOT NULL DEFAULT '', token TEXT NOT NULL,
 pid INTEGER NOT NULL DEFAULT 0, start_token TEXT NOT NULL DEFAULT '',
 launcher_pid INTEGER NOT NULL DEFAULT 0, launcher_token TEXT NOT NULL DEFAULT '',
 session_id TEXT NOT NULL DEFAULT '', pane_id TEXT NOT NULL DEFAULT '',
 heartbeat INTEGER NOT NULL, created INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS agents_ecosystem ON agents(ecosystem_id);
 CREATE INDEX IF NOT EXISTS agents_parent ON agents(ecosystem_id,parent_id);
 CREATE UNIQUE INDEX IF NOT EXISTS agents_live_name ON agents(ecosystem_id,name)
 WHERE state IN ('reserved','starting','running','idle','stopping');`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// write obtains SQLite's writer lock before any admission reads. A deferred
// transaction can race on a read snapshot; BEGIN IMMEDIATE cannot.
func (s *Store) write(ctx context.Context, fn func(*sql.Conn) error) error {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	if err = fn(c); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "COMMIT")
	return err
}

const columns = `id,ecosystem_id,parent_id,name,role,state,task,conclusion,pid,start_token,launcher_pid,launcher_token,session_id,pane_id,heartbeat,created`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Agent, error) {
	var a Agent
	var h, c int64
	err := row.Scan(&a.ID, &a.EcosystemID, &a.ParentID, &a.Name, &a.Role, &a.State, &a.Task, &a.Conclusion, &a.Process.PID, &a.Process.StartToken, &a.Launcher.PID, &a.Launcher.StartToken, &a.Pane.SessionID, &a.Pane.PaneID, &h, &c)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	a.Address = "kh:" + a.ID
	a.Heartbeat = time.Unix(0, h)
	a.Created = time.Unix(0, c)
	return a, err
}
func get(ctx context.Context, c *sql.Conn, id string) (Agent, error) {
	return scan(c.QueryRowContext(ctx, "SELECT "+columns+" FROM agents WHERE id=?", id))
}
func (s *Store) Get(ctx context.Context, id string) (Agent, error) {
	return scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM agents WHERE id=?", id))
}
func validName(name string) bool {
	if name == "" || len(name) > 100 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// taskSummary deliberately discards multiline delegated context. The launch
// layer retains the full prompt privately; discovery only receives a short label.
func taskSummary(task string) string {
	first := strings.SplitN(strings.TrimSpace(task), "\n", 2)[0]
	text := strings.Join(strings.Fields(first), " ")
	runes := []rune(text)
	if len(runes) > 280 {
		return string(runes[:279]) + "…"
	}
	return text
}

func (s *Store) CreateRoot(ctx context.Context, o RootOptions) (Agent, error) {
	if o.Name == "" {
		o.Name = "main"
	}
	if !validName(o.Name) {
		return Agent{}, errors.New("invalid agent name")
	}
	if !o.Process.Valid() {
		return Agent{}, ErrIdentity
	}
	if o.MaxAgents == 0 {
		o.MaxAgents = DefaultMaxAgents
	}
	if o.MaxAgents < 1 {
		return Agent{}, errors.New("max agents must be positive")
	}
	id := randomID()
	now := time.Now().UnixNano()
	var result Agent
	err := s.write(ctx, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, "INSERT INTO ecosystems(id,max_agents) VALUES(?,?)", id, o.MaxAgents); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, `INSERT INTO agents(id,ecosystem_id,parent_id,name,role,state,task,token,pid,start_token,session_id,pane_id,heartbeat,created) VALUES(?,?,'',?,'manager','running',?,'',?,?,?,?,?,?)`, id, id, o.Name, taskSummary(o.Task), o.Process.PID, o.Process.StartToken, o.Pane.SessionID, o.Pane.PaneID, now, now)
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
func (s *Store) Reserve(ctx context.Context, parentID, name, task string) (Reservation, error) {
	if !validName(name) {
		return Reservation{}, errors.New("invalid agent name")
	}
	owner, err := CurrentProcess()
	if err != nil {
		return Reservation{}, err
	}
	id, token := randomID(), randomID()
	now := time.Now().UnixNano()
	var result Agent
	err = s.write(ctx, func(c *sql.Conn) error {
		p, err := get(ctx, c, parentID)
		if err != nil {
			return err
		}
		if p.Role != Manager || (p.State != Running && p.State != Idle) {
			return ErrRole
		}
		var max, live int
		if err = c.QueryRowContext(ctx, "SELECT max_agents FROM ecosystems WHERE id=?", p.EcosystemID).Scan(&max); err != nil {
			return err
		}
		if err = c.QueryRowContext(ctx, "SELECT count(*) FROM agents WHERE ecosystem_id=? AND state IN ('reserved','starting','running','idle','stopping')", p.EcosystemID).Scan(&live); err != nil {
			return err
		}
		if live >= max {
			return fmt.Errorf("%w (%d/%d live, including root)", ErrCapacity, live, max)
		}
		_, err = c.ExecContext(ctx, `INSERT INTO agents(id,ecosystem_id,parent_id,name,role,state,task,token,launcher_pid,launcher_token,heartbeat,created) VALUES(?,?,?,?,'worker','reserved',?,?,?,?,?,?)`, id, p.EcosystemID, parentID, name, taskSummary(task), token, owner.PID, owner.StartToken, now, now)
		if err != nil {
			return err
		}
		result, err = get(ctx, c, id)
		return err
	})
	if err != nil {
		return Reservation{}, err
	}
	return Reservation{Agent: result, Token: token}, nil
}
func changed(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrState
	}
	return err
}
func (s *Store) BeginLaunch(ctx context.Context, id, token string, launcher ProcessIdentity) error {
	if !launcher.Valid() {
		return ErrIdentity
	}
	return changed(s.db.ExecContext(ctx, `UPDATE agents SET state='starting',launcher_pid=?,launcher_token=?,heartbeat=? WHERE id=? AND token=? AND state='reserved'`, launcher.PID, launcher.StartToken, time.Now().UnixNano(), id, token))
}

// ObserveLaunch records the pane process immediately after launch, before child
// startup reaches Claim. This lets reconciliation detect failed rebuild/config
// startup without mistaking a still-live pane for a dead launcher. A child that
// already claimed is accepted only if it is the same exact process.
func (s *Store) ObserveLaunch(ctx context.Context, id, token string, p ProcessIdentity, pane PaneInfo) error {
	if !p.Valid() {
		return ErrIdentity
	}
	return s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, id)
		if err != nil {
			return err
		}
		var stored string
		if err = c.QueryRowContext(ctx, "SELECT token FROM agents WHERE id=?", id).Scan(&stored); err != nil {
			return err
		}
		if stored != token {
			return ErrState
		}
		if a.Process.Valid() {
			if a.Process == p && a.State.Live() {
				return nil
			}
			return ErrState
		}
		if a.State != Starting && a.State != Stopping {
			return ErrState
		}
		return changed(c.ExecContext(ctx, "UPDATE agents SET launcher_pid=?,launcher_token=?,session_id=?,pane_id=? WHERE id=?", p.PID, p.StartToken, pane.SessionID, pane.PaneID, id))
	})
}

// Claim is the child startup gate: it must succeed before model/tool work.
// A cancelled or stopping reservation can never be claimed, even by a late launch.
func (s *Store) Claim(ctx context.Context, id, token string, p ProcessIdentity, pane PaneInfo) (Agent, error) {
	if !p.Valid() {
		return Agent{}, ErrIdentity
	}
	var result Agent
	err := s.write(ctx, func(c *sql.Conn) error {
		if err := changed(c.ExecContext(ctx, `UPDATE agents SET state='running',pid=?,start_token=?,session_id=?,pane_id=?,heartbeat=? WHERE id=? AND token=? AND state='starting'`, p.PID, p.StartToken, pane.SessionID, pane.PaneID, time.Now().UnixNano(), id, token)); err != nil {
			return err
		}
		var err error
		result, err = get(ctx, c, id)
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	return result, nil
}
func (s *Store) CancelReservation(ctx context.Context, id, token string) error {
	return changed(s.db.ExecContext(ctx, "UPDATE agents SET state='cancelled' WHERE id=? AND token=? AND state='reserved'", id, token))
}

// FailLaunch only releases a launch if the caller proved no child was created.
// A timeout or ambiguous tmux error is not such proof and retains the slot.
func (s *Store) FailLaunch(ctx context.Context, id, token string, noChild bool) error {
	if !noChild {
		return errors.New("launch outcome unknown; capacity retained")
	}
	return changed(s.db.ExecContext(ctx, "UPDATE agents SET state='failed' WHERE id=? AND token=? AND state IN ('starting','stopping') AND pid=0 AND pane_id=''", id, token))
}
func (s *Store) SetRole(ctx context.Context, selfID string, role Role) error {
	if role != Manager && role != Worker {
		return errors.New("role must be manager or worker")
	}
	return s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, selfID)
		if err != nil {
			return err
		}
		if a.ParentID == "" && role != Manager {
			return errors.New("root must remain manager")
		}
		if a.State != Running && a.State != Idle {
			return ErrState
		}
		return changed(c.ExecContext(ctx, "UPDATE agents SET role=? WHERE id=?", role, selfID))
	})
}
func (s *Store) Heartbeat(ctx context.Context, id string, p ProcessIdentity, state State) error {
	if !p.Valid() {
		return ErrIdentity
	}
	if state != Running && state != Idle {
		return ErrState
	}
	return changed(s.db.ExecContext(ctx, "UPDATE agents SET state=?,heartbeat=? WHERE id=? AND pid=? AND start_token=? AND state IN ('running','idle')", state, time.Now().UnixNano(), id, p.PID, p.StartToken))
}

// PublishConclusion stores a durable task summary without freeing a process slot.
func (s *Store) PublishConclusion(ctx context.Context, id, summary string) error {
	return changed(s.db.ExecContext(ctx, "UPDATE agents SET conclusion=? WHERE id=?", summary, id))
}
func (s *Store) Resolve(ctx context.Context, ecoID, address string) (Agent, error) {
	key := strings.TrimPrefix(address, "kh:")
	// Stable IDs take precedence over aliases.
	a, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM agents WHERE ecosystem_id=? AND id=?", ecoID, key))
	if !errors.Is(err, ErrNotFound) {
		return a, err
	}
	return scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM agents WHERE ecosystem_id=? AND name=? ORDER BY (state IN ('reserved','starting','running','idle','stopping')) DESC,created DESC,id DESC LIMIT 1", ecoID, key))
}
func (s *Store) List(ctx context.Context, ecoID string) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM agents WHERE ecosystem_id=? ORDER BY created,id", ecoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LiveDescendants counts live descendants, traversing completed ancestors too.
// parent indicates whether a counter should be displayed: roots, managers and
// agents with historical descendants qualify independently of their live count.
func (s *Store) LiveDescendants(ctx context.Context, agentID string) (count int, parent bool, err error) {
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE tree(id,ecosystem_id) AS (
		SELECT id,ecosystem_id FROM agents WHERE id=?
		UNION SELECT a.id,a.ecosystem_id FROM agents a JOIN tree t ON a.parent_id=t.id AND a.ecosystem_id=t.ecosystem_id
	) SELECT a.id,a.state,a.role,a.parent_id FROM agents a JOIN tree t ON a.id=t.id`, agentID)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, parentID string
		var state State
		var role Role
		if err := rows.Scan(&id, &state, &role, &parentID); err != nil {
			return 0, false, err
		}
		if id == agentID {
			found = true
			parent = parent || parentID == "" || role == Manager
		} else {
			parent = true
			if state.Live() {
				count++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotFound
	}
	return count, parent, nil
}

func (s *Store) Stats(ctx context.Context, ecoID string) (Stats, error) {
	var out Stats
	out.States = map[State]int{}
	// One read transaction keeps counts and configuration on the same snapshot.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT max_agents FROM ecosystems WHERE id=?", ecoID).Scan(&out.MaxAgents); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return out, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT state,role,count(*) FROM agents WHERE ecosystem_id=? GROUP BY state,role", ecoID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var state State
		var role Role
		var n int
		if err = rows.Scan(&state, &role, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.States[state] += n
		if state.Live() {
			out.Live += n
			if role == Manager {
				out.Managers += n
			} else {
				out.Workers += n
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Available = max(0, out.MaxAgents-out.Live)
	return out, tx.Commit()
}

// SetMax changes the root's shared limit. Existing processes are never evicted.
func (s *Store) SetMax(ctx context.Context, rootID string, max int) error {
	if max < 1 {
		return errors.New("max agents must be positive")
	}
	return s.write(ctx, func(c *sql.Conn) error {
		a, err := get(ctx, c, rootID)
		if err != nil {
			return err
		}
		if a.ParentID != "" {
			return errors.New("only root configures capacity")
		}
		return changed(c.ExecContext(ctx, "UPDATE ecosystems SET max_agents=? WHERE id=?", max, a.EcosystemID))
	})
}
