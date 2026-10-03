package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var Edit = Tool{
	Name:        "edit",
	Description: "Edit project files. Prefer one coherent batch: {edits:[{path,old,new},...]}. Replacements run in array order against staged text; all are validated before any writes, and each file is written once. Alternatively use the original {path,old,new} format, not both. old must occur exactly once; empty old creates a new file. Group same-file changes in one batch rather than parallel calls. Validation errors change nothing; a write failure can leave earlier files updated and reports them.",
	Params:      editParams(),
	Required:    []string{}, // Either format is accepted; Run checks required fields.
	Run:         runEdit,
}

// One short, cancellable gate protects the read/validate/write cycle, including
// aliases and overlapping batches. Other tools still run concurrently. Agents
// in separate processes must continue to coordinate ownership of shared files.
var editGate = make(chan struct{}, 1)

func editParams() map[string]any {
	fields := map[string]any{
		"path": map[string]any{"type": "string"},
		"old":  map[string]any{"type": "string"},
		"new":  map[string]any{"type": "string"},
	}
	return map[string]any{
		"path": fields["path"], "old": fields["old"], "new": fields["new"],
		"edits": map[string]any{
			"type": "array", "minItems": 1, "maxItems": 128,
			"items": map[string]any{"type": "object", "properties": fields, "required": []string{"path", "old", "new"}},
		},
	}
}

type editChange struct{ Path, Old, New string }

func parseEdits(input json.RawMessage) ([]editChange, bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(input, &envelope); err != nil {
		return nil, false, fmt.Errorf("invalid edit input: %w", err)
	}
	var inputs []json.RawMessage
	raw, batch := envelope["edits"]
	if batch {
		for _, key := range []string{"path", "old", "new"} {
			if _, exists := envelope[key]; exists {
				return nil, true, fmt.Errorf("use edits or path/old/new, not both")
			}
		}
		if err := json.Unmarshal(raw, &inputs); err != nil || len(inputs) == 0 || len(inputs) > 128 {
			return nil, true, fmt.Errorf("edits must contain 1 to 128 changes")
		}
	} else {
		inputs = []json.RawMessage{input}
	}
	changes := make([]editChange, 0, len(inputs))
	for i, raw := range inputs {
		var in struct{ Path, Old, New *string }
		if json.Unmarshal(raw, &in) != nil || in.Path == nil || *in.Path == "" || in.Old == nil || in.New == nil {
			return nil, batch, fmt.Errorf("edit %d: need path, old, new strings", i+1)
		}
		changes = append(changes, editChange{*in.Path, *in.Old, *in.New})
	}
	return changes, batch, nil
}

type editFile struct {
	rel    string
	file   *os.File
	info   os.FileInfo
	text   string
	create bool
}

func runEdit(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	changes, batch, err := parseEdits(input)
	if err != nil {
		return "", err
	}
	select {
	case editGate <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-editGate }()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return "", err
	}
	defer root.Close()
	byPath := make(map[string]*editFile)
	var files []*editFile
	defer func() {
		for _, staged := range files {
			if staged.file != nil {
				staged.file.Close()
			}
		}
	}()

	// Preflight never creates directories or files. Retain each existing file's
	// opened descriptor so a replaced pathname cannot redirect its later write.
	for i, change := range changes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rel, err := editPath(cwd, change.Path)
		if err != nil {
			return "", fmt.Errorf("edit %d: %w", i+1, err)
		}
		staged := byPath[rel]
		if change.Old == "" && staged != nil {
			return "", fmt.Errorf("edit %d (%s): file already exists in this batch", i+1, rel)
		}
		if staged == nil {
			staged, err = stageEditFile(root, rel, change.Old == "", files)
			if err != nil {
				return "", fmt.Errorf("edit %d (%s): %w", i+1, rel, err)
			}
			byPath[rel] = staged
			if staged.rel == rel {
				files = append(files, staged)
			}
		}
		if change.Old == "" {
			staged.text = change.New
			continue
		}
		if n := strings.Count(staged.text, change.Old); n != 1 {
			return "", fmt.Errorf("edit %d (%s): old appears %d times, need exactly 1", i+1, rel, n)
		}
		staged.text = strings.Replace(staged.text, change.Old, change.New, 1)
	}
	for _, staged := range files {
		if staged.create {
			if err := checkEditParents(root, staged.rel, byPath); err != nil {
				return "", err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var completed []string
	for _, staged := range files {
		err := ctx.Err()
		if err == nil {
			err = writeEditFile(root, staged)
		}
		if err != nil {
			return "", fmt.Errorf("write %s failed (it may be partially written); completed files: %v: %w", staged.rel, completed, err)
		}
		completed = append(completed, staged.rel)
		fmt.Fprintln(os.Stderr, "edit", staged.rel)
	}
	if !batch {
		return "ok", nil
	}
	return fmt.Sprintf("ok: %d edits in %d files", len(changes), len(files)), nil
}

func editPath(cwd, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path must be inside the project")
	}
	return rel, nil
}

func stageEditFile(root *os.Root, rel string, create bool, files []*editFile) (*editFile, error) {
	if create {
		// Lstat also catches dangling leaf symlinks: O_EXCL must not be the
		// first place an already-existing path is discovered during commit.
		if _, err := root.Lstat(rel); err == nil {
			return nil, fmt.Errorf("file already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		return &editFile{rel: rel, create: true}, nil
	}
	file, err := root.OpenFile(rel, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("path must be a regular file")
	}
	// Symlink and hardlink aliases share staged text and one final write.
	for _, staged := range files {
		if staged.info != nil && os.SameFile(info, staged.info) {
			file.Close()
			return staged, nil
		}
	}
	b, err := io.ReadAll(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &editFile{rel: rel, file: file, info: info, text: string(b)}, nil
}

func checkEditParents(root *os.Root, rel string, staged map[string]*editFile) error {
	parent := filepath.Dir(rel)
	prefix := ""
	for _, part := range strings.Split(parent, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		if staged[prefix] != nil {
			return fmt.Errorf("%s: parent %s is a file in this batch", rel, prefix)
		}
		info, err := root.Stat(prefix)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("%s: parent %s is not a directory", rel, prefix)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if _, err := root.Lstat(prefix); err == nil {
			return fmt.Errorf("%s: parent %s is a dangling symlink", rel, prefix)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func writeEditFile(root *os.Root, staged *editFile) error {
	if staged.create {
		// Root.MkdirAll requires Go 1.25; resolve every prefix through the
		// Go 1.24 Root methods, including any newly created parent dirs.
		prefix := ""
		for _, part := range strings.Split(filepath.Dir(staged.rel), string(filepath.Separator)) {
			prefix = filepath.Join(prefix, part)
			if err := root.Mkdir(prefix, 0o755); err != nil {
				dir, openErr := root.Open(prefix)
				if openErr != nil {
					return openErr
				}
				info, statErr := dir.Stat()
				dir.Close()
				if statErr != nil {
					return statErr
				}
				if !info.IsDir() {
					return err
				}
			}
		}
		file, err := root.OpenFile(staged.rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		staged.file = file
	}
	if _, err := staged.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := staged.file.WriteString(staged.text); err != nil {
		return err
	}
	if !staged.create {
		if err := staged.file.Truncate(int64(len(staged.text))); err != nil {
			return err
		}
	}
	err := staged.file.Close()
	staged.file = nil
	return err
}
