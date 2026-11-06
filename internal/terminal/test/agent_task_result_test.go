package test

import (
	"strings"
	"testing"

	"kh/internal/terminal"
)

func TestAgentTaskResultAtomicPlainSanitized(t *testing.T) {
	task := "first task\n\x1b[31msecond task\x1b[0m\tline\x07\n" + strings.Repeat("full task ", 100)
	var out resultWriter
	r := terminal.NewRenderer(&out, &out)
	r.AgentTaskResult("worker\nlabel", "task-1\nidentity", &task, "first answer\n\x1b[31msecond answer\x1b[0m\x07", true)
	identity := "worker label (task task-1 identity)"
	want := "ASSIGNED TASK — " + identity + "\nfirst task\nsecond task\tline\n" + strings.Repeat("full task ", 100) + "\n\nWORKER RESULT — " + identity + " [truncated]\nfirst answer\nsecond answer\n\n"
	if out.String() != want || out.writes != 1 {
		t.Fatalf("result=%q writes=%d; want %q", out.String(), out.writes, want)
	}
}

func TestAgentTaskResultMissingVersusEmptyTask(t *testing.T) {
	empty := ""
	for _, tc := range []struct {
		name string
		task *string
		body string
	}{
		{"missing", nil, "Original task unavailable"},
		{"empty", &empty, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out resultWriter
			r := terminal.NewRenderer(&out, &out)
			r.AgentTaskResult("worker", "", tc.task, "\x1b[31m\x1b[0m", false)
			want := "ASSIGNED TASK — worker\n" + tc.body + "\n\nWORKER RESULT — worker [no final conclusion]\n\n\n"
			if out.String() != want || out.writes != 1 {
				t.Fatalf("result=%q writes=%d; want %q", out.String(), out.writes, want)
			}
		})
	}
}
