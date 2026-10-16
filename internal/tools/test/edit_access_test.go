package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kh/internal/config"
	"kh/internal/tools"
)

type editFolderApprover func(context.Context, string) (bool, error)

func (f editFolderApprover) ApproveWrite(ctx context.Context, dir string) (bool, error) {
	return f(ctx, dir)
}

func editAccess(t *testing.T, approve editFolderApprover) *tools.WriteAccess {
	t.Helper()
	cfg := config.Defaults
	cfg.Auto, cfg.Writable = true, nil
	access, err := tools.NewWriteAccess(cfg, approve)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { access.Close() })
	return access
}

func applyAccessEdits(t *testing.T, access *tools.WriteAccess, changes ...batchChange) error {
	t.Helper()
	input, err := json.Marshal(map[string]any{"edits": changes})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tools.EditWithAccess(nil, access).Run(context.Background(), input)
	return err
}

func TestEditOutsideFolderApprovalIsSessionScoped(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	path := filepath.Join(outside, "file.txt")
	writeFixture(t, path, "old")
	calls := 0
	access := editAccess(t, func(_ context.Context, dir string) (bool, error) {
		calls++
		want, err := filepath.EvalSymlinks(outside)
		if err != nil || dir != want {
			t.Errorf("approval dir=%q want=%q err=%v", dir, want, err)
		}
		return true, nil
	})
	if err := applyAccessEdits(t, access, batchChange{path, "old", "new"}); err != nil {
		t.Fatal(err)
	}
	if err := applyAccessEdits(t, access, batchChange{filepath.Join(outside, "nested", "created.txt"), "", "created"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("repeated folder approval: %d", calls)
	}
	requireText(t, path, "new")
	requireText(t, filepath.Join(outside, "nested", "created.txt"), "created")

	freshCalls := 0
	fresh := editAccess(t, func(context.Context, string) (bool, error) {
		freshCalls++
		return false, nil
	})
	if err := applyAccessEdits(t, fresh, batchChange{path, "new", "unauthorized"}); err == nil {
		t.Fatal("a new session inherited the grant")
	}
	if freshCalls != 1 {
		t.Fatalf("new session approvals=%d", freshCalls)
	}
	requireText(t, path, "new")
}

func TestEditCrossFolderBatchPreflightsBeforeWrites(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "project.txt", "old")
	outside := t.TempDir()
	path := filepath.Join(outside, "outside.txt")
	writeFixture(t, path, "old")
	calls := 0
	access := editAccess(t, func(context.Context, string) (bool, error) {
		calls++
		return true, nil
	})
	if err := applyAccessEdits(t, access,
		batchChange{"project.txt", "old", "new"}, batchChange{path, "missing", "new"}); err == nil {
		t.Fatal("invalid outside replacement accepted")
	}
	requireText(t, "project.txt", "old")
	requireText(t, path, "old")
	if err := applyAccessEdits(t, access,
		batchChange{"project.txt", "old", "new"}, batchChange{path, "old", "new"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("approvals=%d", calls)
	}
	requireText(t, "project.txt", "new")
	requireText(t, path, "new")
}

func TestEditDeniedFolderLeavesWholeBatchUnchanged(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "project.txt", "old")
	outside := t.TempDir()
	access := editAccess(t, func(context.Context, string) (bool, error) { return false, nil })
	if err := applyAccessEdits(t, access,
		batchChange{"project.txt", "old", "new"}, batchChange{filepath.Join(outside, "new.txt"), "", "new"}); err == nil {
		t.Fatal("denied folder was edited")
	}
	requireText(t, "project.txt", "old")
	if files, err := os.ReadDir(outside); err != nil || len(files) != 0 {
		t.Fatalf("denied folder changed: %v %v", files, err)
	}
}

func TestEditCrossRootHardlinkAliasesShareOneWrite(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	path := filepath.Join(outside, "shared.txt")
	writeFixture(t, "shared.txt", "old")
	if err := os.Link("shared.txt", path); err != nil {
		t.Fatal(err)
	}
	access := editAccess(t, func(context.Context, string) (bool, error) { return true, nil })
	if err := applyAccessEdits(t, access,
		batchChange{"shared.txt", "old", "middle"}, batchChange{path, "middle", "final"}); err != nil {
		t.Fatal(err)
	}
	requireText(t, "shared.txt", "final")
	requireText(t, path, "final")
}

func TestEditApprovedFolderCannotEscapeThroughSymlink(t *testing.T) {
	t.Chdir(t.TempDir())
	outside, forbidden := t.TempDir(), t.TempDir()
	path := filepath.Join(forbidden, "secret.txt")
	writeFixture(t, path, "old")
	if err := os.Symlink(forbidden, filepath.Join(outside, "escape")); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	access := editAccess(t, func(_ context.Context, dir string) (bool, error) { return dir == canonical, nil })
	if err := access.Require(context.Background(), []string{outside}); err != nil {
		t.Fatal(err)
	}
	if err := applyAccessEdits(t, access,
		batchChange{filepath.Join(outside, "escape", "secret.txt"), "old", "escaped"}); err == nil {
		t.Fatal("symlink escaped approved folder")
	}
	requireText(t, path, "old")
}
