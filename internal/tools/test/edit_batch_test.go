package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kh/internal/tools"
)

type batchChange struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func editBatch(ctx context.Context, changes ...batchChange) (string, error) {
	input, _ := json.Marshal(map[string]any{"edits": changes})
	return tools.Edit.Run(ctx, input)
}

func writeFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
}

func requireText(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != want {
		t.Fatalf("%s: got=%q want=%q err=%v", path, b, want, err)
	}
}

func TestEditBatchOrderedAndBackwardCompatible(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "a.txt", "first second")
	before, err := os.Stat("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	out, err := editBatch(ctx,
		batchChange{"a.txt", "first", "intermediate"},
		batchChange{"./a.txt", "intermediate", "done"},
		batchChange{"a.txt", "second", ""},
		batchChange{"nested/new.txt", "", "new text"},
		batchChange{"nested/new.txt", "new", "created"},
	)
	if err != nil || out != "ok: 5 edits in 2 files" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	requireText(t, "a.txt", "done ")
	requireText(t, "nested/new.txt", "created text")
	after, err := os.Stat("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || after.Mode().Perm() != before.Mode().Perm() {
		t.Fatal("edit replaced the inode or changed permissions")
	}
	out, err = tools.Edit.Run(ctx, []byte(`{"path":"a.txt","old":"done","new":"legacy"}`))
	if err != nil || out != "ok" {
		t.Fatalf("legacy: %q %v", out, err)
	}
	requireText(t, "a.txt", "legacy ")
}

func TestEditBatchValidationChangesNothing(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "a.txt", "original")
	writeFixture(t, "b.txt", "duplicate duplicate")
	for _, failure := range []batchChange{
		{"b.txt", "missing", "x"}, {"b.txt", "duplicate", "x"}, {"a.txt", "", "x"},
		{"missing.txt", "not there", "x"}, {"../outside.txt", "", "x"}, {".", "old", "new"},
	} {
		out, err := editBatch(ctx, batchChange{"a.txt", "original", "changed"}, batchChange{"new/child.txt", "", "new"}, failure)
		if err == nil || out != "" {
			t.Fatalf("accepted %+v: %q %v", failure, out, err)
		}
		requireText(t, "a.txt", "original")
		requireText(t, "b.txt", "duplicate duplicate")
		if _, err := os.Stat("new"); !os.IsNotExist(err) {
			t.Fatalf("preflight created directories: %v", err)
		}
	}
}

func TestEditBatchMalformedInput(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, input := range []string{
		`null`, `[]`, `{}`, `{"edits":null}`, `{"edits":[]}`, `{"edits":{}}`,
		`{"edits":[{"path":"a","old":""}]}`, `{"edits":[{"path":"a","new":"x"}]}`,
		`{"edits":[{"path":"a","old":null,"new":"x"}]}`, `{"edits":[{"path":42,"old":"","new":"x"}]}`,
		`{"path":"a","old":"","new":"x","edits":[]}`, `{"path":"a","new":"x"}`, `{"path":"","old":"","new":"x"}`,
	} {
		if _, err := tools.Edit.Run(ctx, []byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	many := make([]batchChange, 129)
	for i := range many {
		many[i] = batchChange{fmt.Sprintf("file%d", i), "", "x"}
	}
	if _, err := editBatch(ctx, many...); err == nil {
		t.Fatal("oversized batch accepted")
	}
	if entries, err := os.ReadDir("."); err != nil || len(entries) != 0 {
		t.Fatalf("malformed input wrote files: %v %v", entries, err)
	}
	// Root required fields must not prevent the alternative batch format. The
	// array items still have all three fields required in the advertised schema.
	if len(tools.Edit.Required) != 0 {
		t.Fatal("schema still requires legacy fields")
	}
	schema := tools.Edit.Params["edits"].(map[string]any)
	if schema["minItems"] != 1 || schema["maxItems"] != 128 {
		t.Fatal("schema batch bounds missing")
	}
}

func TestEditBatchAliasesShareStagedText(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	writeFixture(t, "real.txt", "one two three")
	if err := os.Symlink("real.txt", "alias.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.Link("real.txt", "hard.txt"); err != nil {
		t.Fatal(err)
	}
	out, err := editBatch(ctx, batchChange{"real.txt", "one", "ONE"}, batchChange{"alias.txt", "two", "TWO"}, batchChange{filepath.Join(project, "hard.txt"), "three", "THREE"})
	if err != nil || out != "ok: 3 edits in 1 files" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	for _, path := range []string{"real.txt", "alias.txt", "hard.txt"} {
		requireText(t, path, "ONE TWO THREE")
	}
}

func TestEditBatchCreateParentValidation(t *testing.T) {
	project, outside := t.TempDir(), t.TempDir()
	t.Chdir(project)
	writeFixture(t, "existing", "old")
	if err := os.Symlink(outside, "escape"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", "dangling"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"escape/new/child", "existing/child", "dangling/child", "dangling"} {
		_, err := editBatch(ctx, batchChange{"existing", "old", "changed"}, batchChange{path, "", "x"})
		if err == nil {
			t.Fatalf("accepted %s", path)
		}
		requireText(t, "existing", "old")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside changed: %v %v", entries, err)
	}
	_, err := editBatch(ctx, batchChange{"parent", "", "file"}, batchChange{"parent/child", "", "x"})
	if err == nil {
		t.Fatal("staged file used as parent")
	}
	if _, err := os.Stat("parent"); !os.IsNotExist(err) {
		t.Fatalf("preflight wrote parent: %v", err)
	}
}

func TestConcurrentEditsPreserveAllChanges(t *testing.T) {
	t.Chdir(t.TempDir())
	const count = 32
	var original strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&original, "token%02d\n", i)
	}
	writeFixture(t, "shared.txt", original.String())
	if err := os.Symlink("shared.txt", "alias.txt"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errors := make(chan error, count)
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			path := "shared.txt"
			if i%2 == 1 {
				path = "alias.txt"
			}
			input, _ := json.Marshal(batchChange{path, fmt.Sprintf("token%02d", i), fmt.Sprintf("done%02d", i)})
			_, err := tools.Edit.Run(ctx, input)
			errors <- err
		}(i)
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireText(t, "shared.txt", strings.ReplaceAll(original.String(), "token", "done"))
}

type pausedEditContext struct {
	context.Context
	calls            atomic.Int32
	entered, release chan struct{}
}

func (c *pausedEditContext) Err() error {
	// The second check occurs with the edit gate held. Pause through the public
	// Context contract rather than exposing a testing hook in the tool.
	if c.calls.Add(1) == 2 {
		close(c.entered)
		<-c.release
	}
	return c.Context.Err()
}

func TestEditCancellationWhileQueued(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "a", "old")
	first := &pausedEditContext{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := editBatch(first, batchChange{"a", "old", "new"}); done <- err }()
	select {
	case <-first.entered:
	case <-time.After(time.Second):
		t.Fatal("first edit did not acquire gate")
	}
	queued, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err := editBatch(queued, batchChange{"other", "", "x"})
	cancel()
	close(first.release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued edit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	requireText(t, "a", "new")
	if _, err := os.Stat("other"); !os.IsNotExist(err) {
		t.Fatalf("canceled edit wrote: %v", err)
	}
}

type cancelAfterWriteContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelAfterWriteContext) Err() error {
	if b, _ := os.ReadFile("a"); string(b) == "new" {
		c.cancel()
	}
	return c.Context.Err()
}

func TestEditBatchReportsPartialCommit(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "a", "old")
	writeFixture(t, "b", "old")
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	out, err := editBatch(cancelAfterWriteContext{base, cancel}, batchChange{"a", "old", "new"}, batchChange{"b", "old", "new"})
	if out != "" || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "completed files: [a]") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	requireText(t, "a", "new")
	requireText(t, "b", "old")
}

func TestEditCanceledBeforeValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := editBatch(canceled, batchChange{"a", "", "x"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat("a"); !os.IsNotExist(err) {
		t.Fatalf("canceled edit wrote: %v", err)
	}
}
