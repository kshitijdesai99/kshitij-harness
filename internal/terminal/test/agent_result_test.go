package test

import (
	"kh/internal/terminal"
	"strings"
	"testing"
)

func TestAgentResultEmptyConclusionLabel(t *testing.T) {
	for _, body := range []string{"", " \t\n", "\x1b[31m\x1b[0m"} {
		t.Run(body, func(t *testing.T) {
			var out resultWriter
			r := terminal.NewRenderer(&out, &out)
			r.AgentResult("worker", "task-1", body, false)
			clean := body
			if strings.Contains(body, "\x1b") {
				clean = ""
			}
			want := "WORKER RESULT — worker (task task-1) [no final conclusion]\n" + clean + "\n\n"
			if out.String() != want || out.writes != 1 {
				t.Fatalf("result=%q writes=%d; want %q", out.String(), out.writes, want)
			}
		})
	}
}

type resultWriter struct {
	strings.Builder
	writes int
}

func (w *resultWriter) Write(p []byte) (int, error) { w.writes++; return w.Builder.Write(p) }

func TestAgentResultAtomicPlainSanitized(t *testing.T) {
	var out resultWriter
	r := terminal.NewRenderer(&out, &out)
	r.AgentResult("worker\nlabel", "task-1", "first\n\x1b[31msecond\x1b[0m\tline\x07", true)
	want := "WORKER RESULT — worker label (task task-1) [truncated]\nfirst\nsecond\tline\n\n"
	if out.String() != want || out.writes != 1 {
		t.Fatalf("result=%q writes=%d", out.String(), out.writes)
	}
}
