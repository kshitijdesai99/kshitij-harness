package test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Keep regression tests out of production package roots.
func TestTestFilesLiveUnderTestFolders(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), "_test.go") && filepath.Base(filepath.Dir(path)) != "test" {
			t.Errorf("test belongs in a test/ directory: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
