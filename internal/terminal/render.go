package terminal

import (
	"fmt"
	"io"
	"strings"
)

// Renderer owns chat presentation. Model adapters emit text, not ANSI codes.
type Renderer struct {
	Out      io.Writer
	Err      io.Writer
	Activity *Activity
	reply    *markdownReply // shared when Renderer values are copied into adapters
}

// NewRenderer enables Markdown presentation only on an interactive, capable
// terminal. Literal Renderer values retain the original plain streaming API.
func NewRenderer(out, err io.Writer) Renderer {
	r := Renderer{Out: out, Err: err}
	r.reply = newMarkdownReply(out)
	return r
}

// FlushReply finishes an assistant message, including partial/cancelled replies.
// Adapters call this at semantic boundaries, not at arbitrary token boundaries.
func (r Renderer) FlushReply() {
	if r.reply != nil {
		r.reply.flush()
	}
}

func (r Renderer) Reply(text string) {
	if r.Activity != nil && strings.TrimSpace(text) != "" {
		r.Activity.Set("responding")
	}
	if r.reply != nil {
		r.reply.write(text, false)
		return
	}
	fmt.Fprint(r.Out, Color(r.Out, Response, text))
}

// Commentary distinguishes intermediate progress from the final answer.
func (r Renderer) Commentary(text string) {
	if r.Activity != nil && strings.TrimSpace(text) != "" {
		r.Activity.Set("responding")
	}
	if r.reply != nil {
		r.reply.write(text, true)
		return
	}
	fmt.Fprint(r.Out, Color(r.Out, Harness, text))
}

// Progress updates titles without streaming private operations into the chat.
func (r Renderer) Progress(phase string) {
	if r.Activity != nil {
		r.Activity.Set(phase)
	}
}

func (r Renderer) Statistics(text string) {
	r.FlushReply()
	fmt.Fprintln(r.Err, Color(r.Err, Harness, text))
}

// Task prints a one-time assignment in scrollback, not a pinned header.
// Keep the full literal task: newlines are preserved and the terminal wraps it.
func (r Renderer) Task(name, text string) {
	r.FlushReply()
	fmt.Fprint(r.Out, Color(r.Out, AssignedTask, "ASSIGNED TASK — "+name+"\n"+text+"\n\n"))
}

func (r Renderer) Query(text string) {
	r.FlushReply()
	fmt.Fprintln(r.Out, colorQuery(r.Out, text, "> "))
}

func (r Renderer) Action(text string) {
	r.FlushReply()
	if progress := newMarkdownReply(r.Err); progress != nil {
		progress.commentary = true
		progress.present(cleanReply(text) + "\n")
		return
	}
	fmt.Fprintln(r.Err, Color(r.Err, Harness, text))
}

// AgentResult prints one labelled block without touching the active reply.
// A single coordinated write preserves an editor draft while results arrive.
func (r Renderer) AgentResult(name, taskID, conclusion string, truncated bool) {
	fmt.Fprint(r.Out, Color(r.Out, WorkerResult, agentResultBlock(name, taskID, conclusion, truncated)))
}

// AgentTaskResult appends the full assignment and result in one coordinated write,
// without flushing a pending assistant reply or disturbing the editor draft.
func (r Renderer) AgentTaskResult(name, taskID string, task *string, conclusion string, truncated bool) {
	identity := strings.ReplaceAll(cleanReply(name), "\n", " ")
	if taskID != "" {
		identity += " (task " + strings.ReplaceAll(cleanReply(taskID), "\n", " ") + ")"
	}
	body := "Original task unavailable"
	if task != nil {
		body = cleanReply(*task)
	}
	assignment := "ASSIGNED TASK — " + identity + "\n" + body + "\n\n"
	block := Color(r.Out, AssignedTask, assignment) + Color(r.Out, WorkerResult, agentResultBlock(name, taskID, conclusion, truncated))
	fmt.Fprint(r.Out, block)
}

func agentResultBlock(name, taskID, conclusion string, truncated bool) string {
	label := "WORKER RESULT — " + strings.ReplaceAll(cleanReply(name), "\n", " ")
	if taskID != "" {
		label += " (task " + strings.ReplaceAll(cleanReply(taskID), "\n", " ") + ")"
	}
	if truncated {
		label += " [truncated]"
	}
	body := cleanReply(conclusion)
	if strings.TrimSpace(body) == "" {
		label += " [no final conclusion]"
	}
	return label + "\n" + body + "\n\n"
}

// Warning keeps actionable diagnostics distinct from routine harness notices.
func (r Renderer) Warning(text string) {
	r.FlushReply()
	fmt.Fprintln(r.Err, Color(r.Err, LightOrange, text))
}

func (r Renderer) Notice(text string) {
	r.FlushReply()
	fmt.Fprintln(r.Err, Color(r.Err, Harness, text))
}
