package test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"kh/internal/terminal"
)

func TestWriteApprovalExplicitAnswers(t *testing.T) {
	dir := t.TempDir()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"yes\n", true}, {"Y\n", true}, {"no\n", false},
		{"N\n", false}, {"\n", false}, {"", false},
	} {
		t.Run(strconv.Quote(tc.input), func(t *testing.T) {
			var output strings.Builder
			c := terminal.NewConsole(strings.NewReader(tc.input), io.Discard, &output)
			defer c.Close()
			c.Start()
			ok, err := c.ApproveWrite(context.Background(), dir)
			if err != nil || ok != tc.want {
				t.Fatalf("ok=%v err=%v want=%v", ok, err, tc.want)
			}
			if text := output.String(); !strings.Contains(text, strconv.QuoteToASCII(canonical)) || !strings.Contains(text, "session-only write access") || !strings.Contains(text, "[y/N]") {
				t.Fatalf("unclear prompt: %q", text)
			}
		})
	}
}

func TestWriteApprovalKeepsSteeringAndHandlesLocalCommands(t *testing.T) {
	c := terminal.NewConsole(strings.NewReader("/fork\n/new\nnew\nchange direction\nyes\nno\n"), io.Discard, io.Discard)
	defer c.Close()
	commands := make(chan string, 2)
	c.SetCommandHandler(func(line string) bool {
		if line != "/fork" && line != "/new" {
			return false
		}
		commands <- line
		return true
	})
	c.Start()
	dir := t.TempDir()
	ok, err := c.ApproveWrite(context.Background(), dir)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(commands) != 2 {
		t.Fatalf("local commands handled: %d", len(commands))
	}
	if held := c.Held(); !reflect.DeepEqual(held, []string{"new", "change direction"}) {
		t.Fatalf("held=%q", held)
	}
	// A previous grant is not an automatic approval on a subsequent call.
	if ok, err := c.ApproveWrite(context.Background(), dir); err != nil || ok {
		t.Fatalf("second approval ok=%v err=%v", ok, err)
	}
}

func TestWriteApprovalCanonicalAndControlSafe(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "folder\x1b[31m\n\t\u202e")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "new/subdirectory"} {
		var output strings.Builder
		c := terminal.NewConsole(strings.NewReader("yes\n"), io.Discard, &output)
		defer c.Close()
		c.Start()
		if ok, err := c.ApproveWrite(context.Background(), filepath.Join(link, suffix)); err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		text := output.String()
		if !strings.Contains(text, strconv.QuoteToASCII(filepath.Join(canonical, suffix))) || strings.ContainsAny(text, "\x1b\t\u202e") || strings.Contains(text, link) {
			t.Fatalf("unsafe or noncanonical prompt: %q", text)
		}
	}
}

type approvalPrompts chan string

func (p approvalPrompts) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "[y/N]") {
		p <- string(b)
	}
	return len(b), nil
}

func waitApprovalPrompt(t *testing.T, prompts approvalPrompts) string {
	t.Helper()
	select {
	case prompt := <-prompts:
		return prompt
	case <-time.After(time.Second):
		t.Fatal("no approval prompt")
		return ""
	}
}

func TestWriteApprovalCancellationAndClose(t *testing.T) {
	for _, closeConsole := range []bool{false, true} {
		t.Run(strconv.FormatBool(closeConsole), func(t *testing.T) {
			in, writer := io.Pipe()
			defer in.Close()
			defer writer.Close()
			prompts := make(approvalPrompts, 8)
			c := terminal.NewConsole(in, io.Discard, prompts)
			defer c.Close()
			c.Start()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dir := t.TempDir()
			result := make(chan error, 1)
			go func() {
				ok, err := c.ApproveWrite(ctx, dir)
				if ok {
					result <- errors.New("unexpected grant")
					return
				}
				result <- err
			}()
			waitApprovalPrompt(t, prompts)
			if closeConsole {
				c.Close()
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if err == nil || (!closeConsole && !errors.Is(err, context.Canceled)) {
					t.Fatalf("err=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("approval did not stop")
			}
			if ok, err := c.ApproveWrite(ctx, dir); ok || err == nil {
				t.Fatalf("already stopped: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestWriteAndShellApprovalsShareGate(t *testing.T) {
	for _, writeFirst := range []bool{false, true} {
		t.Run(strconv.FormatBool(writeFirst), func(t *testing.T) {
			in, writer := io.Pipe()
			defer in.Close()
			defer writer.Close()
			prompts := make(approvalPrompts, 8)
			c := terminal.NewConsole(in, io.Discard, prompts)
			defer c.Close()
			c.Start()
			dir := t.TempDir()
			approve := func(ctx context.Context, write bool) (bool, error) {
				if write {
					return c.ApproveWrite(ctx, dir)
				}
				return c.Approve(ctx, "command", false)
			}
			first := make(chan bool, 1)
			go func() { ok, err := approve(context.Background(), writeFirst); first <- ok && err == nil }()
			waitApprovalPrompt(t, prompts)
			// A queued prompt must remain cancellable without displaying itself.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if ok, err := approve(ctx, !writeFirst); ok || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("queued ok=%v err=%v", ok, err)
			}
			if len(prompts) != 0 {
				t.Fatal("queued approval displayed before the active answer")
			}
			second := make(chan bool, 1)
			go func() { ok, err := approve(context.Background(), !writeFirst); second <- !ok && err == nil }()
			if _, err := io.WriteString(writer, "yes\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case ok := <-first:
				if !ok {
					t.Fatal("first approval not granted")
				}
			case <-time.After(time.Second):
				t.Fatal("first approval did not finish")
			}
			waitApprovalPrompt(t, prompts)
			if _, err := io.WriteString(writer, "no\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case ok := <-second:
				if !ok {
					t.Fatal("second approval not denied")
				}
			case <-time.After(time.Second):
				t.Fatal("second approval did not finish")
			}
		})
	}
}
