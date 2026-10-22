package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	MaxProcedure   = 128 * 1024 // bytes, not characters
	MaxAssets      = 16
	MaxAssetSource = 128 * 1024
)

// Reference is a portable procedure. Only Note's compact description and aliases
// enter ordinary recall; the full procedure and inert assets live separately.
type Reference struct {
	Note        Note
	Origin      *Note // original exported provenance, separate from the local import note
	ReferenceID string
	Version     int
	Aliases     []string
	Procedure   string
	Assets      []Asset
}

// Asset is inert source data. The store never executes it. Interpreter names
// are labels, not shell commands or executable paths.
type Asset struct{ Name, Interpreter, Source string }

// encodeOrigin bounds untrusted provenance before marshaling, validates it like
// an ordinary note, and checks every field (including owner/repo/refs) for secrets.
func encodeOrigin(n *Note) (string, error) {
	if n == nil {
		return "", nil
	}
	const maxOrigin = 16 * 1024
	if len(n.Refs) > 32 {
		return "", fmt.Errorf("memory: too many origin refs")
	}
	size := 0
	for _, value := range append([]string{n.Repo, n.Kind, n.Key, n.Text, n.Owner, n.Source, n.ContextSummary}, n.Refs...) {
		size += len(value)
		if size > maxOrigin || !utf8.ValidString(value) || Secret(value) {
			return "", fmt.Errorf("memory: invalid or oversized origin metadata")
		}
	}
	if err := valid(*n); err != nil {
		return "", err
	}
	data, err := json.Marshal(n)
	if err != nil {
		return "", err
	}
	if len(data) > maxOrigin || Secret(string(data)) {
		return "", fmt.Errorf("memory: invalid or oversized origin metadata")
	}
	return string(data), nil
}

func validateReference(r *Reference) error {
	if r.Note.Kind == "" {
		r.Note.Kind = "reference"
	}
	if r.Note.Kind != "reference" || r.Note.Forgotten {
		return fmt.Errorf("memory: PutReference requires a live reference")
	}
	if strings.TrimSpace(r.Procedure) == "" || len(r.Procedure) > MaxProcedure || !utf8.ValidString(r.Procedure) {
		return fmt.Errorf("memory: procedure must be nonempty UTF-8, at most %d bytes", MaxProcedure)
	}
	if r.ReferenceID != "" {
		id, err := uuid.Parse(r.ReferenceID)
		if err != nil || id.String() != r.ReferenceID || id == uuid.Nil {
			return fmt.Errorf("memory: reference ID must be a nonzero canonical UUID")
		}
	}
	if len(r.Aliases) > 32 || len(r.Assets) > MaxAssets {
		return fmt.Errorf("memory: too many aliases or assets")
	}
	seen := map[string]bool{}
	for _, alias := range r.Aliases {
		if !utf8.ValidString(alias) || alias != strings.TrimSpace(alias) || alias == "" || len(alias) > 100 || strings.ContainsAny(alias, "\n\r\x00") || seen[alias] {
			return fmt.Errorf("memory: aliases must be unique short lines")
		}
		seen[alias] = true
		if Secret(alias) {
			return fmt.Errorf("memory: refusing reference secret")
		}
	}
	seen = map[string]bool{}
	for _, a := range r.Assets {
		if !utf8.ValidString(a.Name) || a.Name == "" || len(a.Name) > 100 || a.Name == "." || a.Name == ".." || path.Base(a.Name) != a.Name || strings.ContainsAny(a.Name, "\\\x00\n\r") || seen[a.Name] {
			return fmt.Errorf("memory: asset names must be unique basenames")
		}
		seen[a.Name] = true
		if !one(a.Interpreter, []string{"sh", "bash", "zsh", "python", "python3", "node", "ruby", "perl", "osascript"}) {
			return fmt.Errorf("memory: unsupported asset interpreter")
		}
		if len(a.Source) == 0 || len(a.Source) > MaxAssetSource || !utf8.ValidString(a.Source) {
			return fmt.Errorf("memory: asset source must be UTF-8, at most %d bytes", MaxAssetSource)
		}
		if Secret(a.Name) || Secret(a.Source) || Secret(a.Interpreter) {
			return fmt.Errorf("memory: refusing reference secret")
		}
	}
	if Secret(r.Procedure) {
		return fmt.Errorf("memory: refusing reference secret")
	}
	if len(r.Aliases) > 0 {
		r.Note.Text += "\nAliases: " + strings.Join(r.Aliases, ", ")
	}
	return valid(r.Note)
}

// PutReference atomically appends compact metadata and its full payload. The
// topic's portable ID cannot change; version numbers advance across tombstones.
// On first import an explicit version is retained; ordinary creations start at 1.
func (s *Store) PutReference(ctx context.Context, r Reference, expect int64) (Reference, error) {
	// Only remove an alias suffix we generated for this exact saved version.
	// Arbitrary description text containing "Aliases:" is user data.
	if r.Note.ID > 0 {
		old, ok, err := s.Reference(ctx, r.Note.Repo, r.Note.ID)
		if err != nil {
			return Reference{}, err
		}
		if ok && old.Note.Key == r.Note.Key && old.Note.Text == r.Note.Text && len(old.Aliases) > 0 {
			r.Note.Text = strings.TrimSuffix(r.Note.Text, "\nAliases: "+strings.Join(old.Aliases, ", "))
		}
	}
	if err := validateReference(&r); err != nil {
		return Reference{}, err
	}
	origin, err := encodeOrigin(r.Origin)
	if err != nil {
		return Reference{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Reference{}, err
	}
	defer tx.Rollback()
	_, saved, err := s.addTx(ctx, tx, r.Note, expect)
	if err != nil {
		return Reference{}, err
	}
	var oldID string
	var version int
	err = tx.QueryRowContext(ctx, `SELECT p.reference_id,p.version FROM reference_payloads p JOIN notes n ON n.id=p.note_id WHERE n.repo=? AND n.key=? ORDER BY n.created_at DESC,n.id DESC LIMIT 1`, r.Note.Repo, r.Note.Key).Scan(&oldID, &version)
	if err != nil && err != sql.ErrNoRows {
		return Reference{}, err
	}
	if err == nil {
		if r.ReferenceID != "" && r.ReferenceID != oldID {
			return Reference{}, fmt.Errorf("memory: reference ID is immutable")
		}
		if version == int(^uint(0)>>1) {
			return Reference{}, fmt.Errorf("memory: reference version exhausted")
		}
		r.ReferenceID = oldID
		r.Version = version + 1
	} else {
		if r.ReferenceID == "" {
			r.ReferenceID = uuid.NewString()
		}
		if r.Note.Source != "import" || r.Version < 1 {
			r.Version = 1
		}
	}
	var claimed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reference_payloads p JOIN notes n ON n.id=p.note_id WHERE n.repo=? AND n.key<>? AND p.reference_id=?)`, r.Note.Repo, r.Note.Key, r.ReferenceID).Scan(&claimed); err != nil {
		return Reference{}, err
	}
	if claimed {
		return Reference{}, fmt.Errorf("memory: reference ID already belongs to another topic")
	}
	aliases, err := json.Marshal(r.Aliases)
	if err != nil {
		return Reference{}, err
	}
	assets, err := json.Marshal(r.Assets)
	if err != nil {
		return Reference{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO reference_payloads(note_id,reference_id,version,aliases,procedure,assets,origin) VALUES(?,?,?,?,?,?,?)`, saved.ID, r.ReferenceID, r.Version, string(aliases), r.Procedure, string(assets), origin)
	if err != nil {
		return Reference{}, err
	}
	if err = tx.Commit(); err != nil {
		return Reference{}, err
	}
	r.Note = saved
	return r, nil
}

// Reference gets one scoped version, including superseded versions and those
// whose topic has subsequently been forgotten. Tombstones have no payload.
func (s *Store) Reference(ctx context.Context, scope string, id int64) (Reference, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM notes WHERE id=? AND repo IN (?, 'global') AND kind='reference'`, id, scope)
	if err != nil {
		return Reference{}, false, err
	}
	ns, err := scan(rows)
	if err != nil || len(ns) == 0 {
		return Reference{}, false, err
	}
	r := Reference{Note: ns[0]}
	var aliases, assets, origin string
	err = s.db.QueryRowContext(ctx, `SELECT reference_id,version,aliases,procedure,assets,origin FROM reference_payloads WHERE note_id=?`, id).Scan(&r.ReferenceID, &r.Version, &aliases, &r.Procedure, &assets, &origin)
	if err == sql.ErrNoRows {
		return Reference{}, false, nil
	}
	if err != nil {
		return Reference{}, false, err
	}
	if err = json.Unmarshal([]byte(aliases), &r.Aliases); err != nil {
		return Reference{}, false, err
	}
	if err = json.Unmarshal([]byte(assets), &r.Assets); err != nil {
		return Reference{}, false, err
	}
	if origin != "" {
		if err = json.Unmarshal([]byte(origin), &r.Origin); err != nil {
			return Reference{}, false, err
		}
	}
	return r, true, nil
}

// LatestReference resolves exact, case-sensitive names or aliases among live
// references. Repository matches precede global matches; multiple matches in
// the winning tier are an error, including name-versus-alias collisions.
func (s *Store) LatestReference(ctx context.Context, scope, name string) (Reference, bool, error) {
	rows, err := s.db.QueryContext(ctx, latest+` SELECT `+prefixed("l.")+` FROM latest l
        JOIN reference_payloads p ON p.note_id=l.id
        WHERE l.kind='reference' AND l.forgotten=0
        AND (l.key=? OR EXISTS (SELECT 1 FROM json_each(p.aliases) WHERE value=?))`, scope, name, name)
	if err != nil {
		return Reference{}, false, err
	}
	matches, err := scan(rows)
	if err != nil {
		return Reference{}, false, err
	}
	var selected []Note
	for _, n := range matches {
		if n.Repo == scope {
			selected = append(selected, n)
		}
	}
	if len(selected) == 0 {
		selected = matches
	}
	if len(selected) > 1 {
		return Reference{}, false, fmt.Errorf("memory: ambiguous reference name or alias %q", name)
	}
	if len(selected) == 0 {
		return Reference{}, false, nil
	}
	return s.Reference(ctx, scope, selected[0].ID)
}
