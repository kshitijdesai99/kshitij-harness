package test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEditCrossDirectoryWithoutGrants(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	path := filepath.Join(outside, "file.txt")
	writeFixture(t, path, "old")
	if _, err := editBatch(ctx, batchChange{path, "old", "new"}, batchChange{filepath.Join(outside, "nested", "created.txt"), "", "created"}); err != nil {
		t.Fatal(err)
	}
	requireText(t, path, "new")
	requireText(t, filepath.Join(outside, "nested", "created.txt"), "created")
}

func TestEditCrossDirectoryPreflightsWholeBatch(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "project.txt", "old")
	path := filepath.Join(t.TempDir(), "outside.txt")
	writeFixture(t, path, "old")
	if _, err := editBatch(ctx, batchChange{"project.txt", "old", "new"}, batchChange{path, "missing", "new"}); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	requireText(t, "project.txt", "old")
	requireText(t, path, "old")
}

func TestEditCrossDirectoryHardlinkAliases(t *testing.T) {
	t.Chdir(t.TempDir())
	path := filepath.Join(t.TempDir(), "shared.txt")
	writeFixture(t, "shared.txt", "old")
	if err := os.Link("shared.txt", path); err != nil {
		t.Fatal(err)
	}
	if _, err := editBatch(ctx, batchChange{"shared.txt", "old", "middle"}, batchChange{path, "middle", "final"}); err != nil {
		t.Fatal(err)
	}
	requireText(t, path, "final")
	requireText(t, "shared.txt", "final")
}
