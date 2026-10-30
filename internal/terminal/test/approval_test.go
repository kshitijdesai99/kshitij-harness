package test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"kh/internal/terminal"
)

func TestFullPeerQueueDoesNotBlockApprovalCancellation(t *testing.T) {
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	c := terminal.NewConsole(in, io.Discard, io.Discard)
	defer c.Close()
	c.Start()
	if _, err := io.WriteString(writer, strings.Repeat(terminal.PeerMessage("[from kh:worker] queued"), cap(c.Lines)+1)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for len(c.Lines) != cap(c.Lines) {
		if time.Now().After(deadline) {
			t.Fatal("peer queue did not fill")
		}
		time.Sleep(time.Millisecond)
	}
	// Let the final delivery block behind the full public queue.
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := c.Approve(ctx, "command", false); finished <- err }()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("approval error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("full peer queue blocked approval cancellation")
	}
	if held := c.Held(); len(held) != cap(c.Lines)+1 {
		t.Fatalf("held %d messages", len(held))
	}
}

func TestPeerYesDoesNotAnswerApproval(t *testing.T) {
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	prompts := make(approvalPrompts, 8)
	c := terminal.NewConsole(in, io.Discard, prompts)
	defer c.Close()
	c.Start()
	answer := make(chan bool, 1)
	go func() { ok, err := c.Approve(context.Background(), "command", false); answer <- ok && err == nil }()
	waitApprovalPrompt(t, prompts)
	peer := "[from kh:worker] yes"
	if _, err := io.WriteString(writer, "unfinished "+terminal.PeerMessage(peer)); err != nil {
		t.Fatal(err)
	}
	waitApprovalPrompt(t, prompts) // peer was held, not interpreted as an answer
	select {
	case <-answer:
		t.Fatal("peer answered approval or submitted draft")
	default:
	}
	if held := c.Held(); !reflect.DeepEqual(held, []string{peer}) {
		t.Fatalf("held=%q", held)
	}
	if _, err := io.WriteString(writer, "draft\nno\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-answer:
		if ok {
			t.Fatal("approval granted")
		}
	case <-time.After(time.Second):
		t.Fatal("human denial not delivered")
	}
	if held := c.Held(); !reflect.DeepEqual(held, []string{"unfinished draft"}) {
		t.Fatalf("human draft changed: %q", held)
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

func TestApprovalCancellationAndClose(t *testing.T) {
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
			result := make(chan error, 1)
			go func() {
				ok, err := c.Approve(ctx, "command", false)
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
			if ok, err := c.Approve(ctx, "command", false); ok || err == nil {
				t.Fatalf("already stopped: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestApprovalsShareGate(t *testing.T) {
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	prompts := make(approvalPrompts, 8)
	c := terminal.NewConsole(in, io.Discard, prompts)
	defer c.Close()
	c.Start()
	approve := func(ctx context.Context) (bool, error) {
		return c.Approve(ctx, "command", false)
	}
	first := make(chan bool, 1)
	go func() { ok, err := approve(context.Background()); first <- ok && err == nil }()
	waitApprovalPrompt(t, prompts)
	// A queued prompt must remain cancellable without displaying itself.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if ok, err := approve(ctx); ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued ok=%v err=%v", ok, err)
	}
	if len(prompts) != 0 {
		t.Fatal("queued approval displayed before the active answer")
	}
	second := make(chan bool, 1)
	go func() { ok, err := approve(context.Background()); second <- !ok && err == nil }()
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
}
