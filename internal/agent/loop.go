// Package agent is the loop: ask the model, run its tools, repeat.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"kh/internal/image"
	"kh/internal/provider"
	"kh/internal/tools"
)

// Stepper is the only model behavior the loop requires. Persistence, model
// switching and metrics belong to the application, not tool orchestration.
// Step must promptly honor context cancellation and roll back partial output
// and unpaired calls from failed/cancelled steps before returning. Steering
// waits for this cleanup before sending another message to the same model.
type Stepper interface {
	Step(context.Context, string, []provider.Result) ([]provider.Call, error)
}

// Runner composes model, tools and user input without a concrete backend.
type Runner struct {
	Model    Stepper
	Tools    []tools.Tool
	Input    <-chan string
	Held     func() []string
	Notice   func(string)
	Activity func(string)
	Observe  func([]provider.Call, []provider.Result) // sees each batch of tool calls, e.g. for memory hooks
	// BeforeMessage runs with the caller's context for each nonempty user message,
	// after image processing and before the model. An error aborts the turn.
	BeforeMessage func(context.Context, string) error
	// ExpandMessage resolves user-invoked project commands after the user hook.
	// It runs for initial, steering, and queued input, never tool-result steps.
	ExpandMessage func(string) string
}

// Run executes one task. Steering interrupts a reply, but lets running tools
// finish so their results can still be paired with the calls in history.
func (r Runner) Run(ctx context.Context, task string) error {
	p, ts, steer := r.Model, r.Tools, r.Input
	user, results := task, []provider.Result(nil)
	messagePending := user != ""
	defer func() {
		if messagePending {
			// A pre-Step hook can fail after staging an image or memory. Do
			// not attach that abandoned input to an unrelated later message.
			if a, ok := p.(provider.ImageAttacher); ok {
				a.AttachImage("")
			}
			if m, ok := p.(provider.MemorySetter); ok {
				m.SetMemory("")
			}
		}
		if len(results) == 0 {
			return
		}
		// A hook/image error or cancellation must not strand completed tool
		// calls. Step's contract retains supplied results even when cancelled.
		flush, cancel := context.WithCancel(ctx)
		cancel()
		_, _ = p.Step(flush, "", results)
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		messagePending = user != ""
		var err error
		user, err = withPastedImage(p, user)
		if err != nil {
			return err
		}
		if user != "" && r.BeforeMessage != nil {
			if err := r.BeforeMessage(ctx, user); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if user != "" && r.ExpandMessage != nil {
			user = r.ExpandMessage(user)
		}
		if r.Activity != nil {
			r.Activity("waiting for model")
		}
		messagePending = false // provider now owns any staged input
		calls, line, err := step(ctx, p, user, results, steer, r.Notice)
		results = nil // supplied outputs now belong to provider history
		if err != nil {
			return err
		}
		if line != "" { // steered: the cut-off reply was dropped, go again with the line
			user, results = line, nil
			continue
		}
		if len(calls) == 0 {
			return nil
		}
		if r.Activity != nil {
			phase := "running " + calls[0].Name
			if len(calls) > 1 {
				phase = fmt.Sprintf("running tools (%d)", len(calls))
			}
			r.Activity(phase)
		}
		results = runAll(ctx, ts, calls)
		if r.Observe != nil {
			r.Observe(calls, results)
		}
		var held []string
		if r.Held != nil {
			held = r.Held()
		}
		user = queued(held, steer)
	}
}

var pastedImage = regexp.MustCompile(`\[\[kh:image:([a-f0-9]{32})\]\]`)

func withPastedImage(p Stepper, text string) (string, error) {
	if strings.Contains(text, image.PasteMarker) {
		return "", fmt.Errorf("this image marker predates clipboard capture; press Ctrl-V again")
	}
	matches := pastedImage.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text, nil
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("paste one image per message")
	}
	attacher, ok := p.(provider.ImageAttacher)
	if !ok {
		return "", fmt.Errorf("this provider does not support images")
	}
	url, err := image.Take(matches[0][1])
	if err != nil {
		return "", err
	}
	attacher.AttachImage(url)
	text = strings.TrimSpace(strings.Replace(text, matches[0][0], "", 1))
	if text == "" {
		text = "Describe this image."
	}
	return text, nil
}

// step runs one model reply, but gives up on it as soon as the user types a
// line, and returns that line instead.
func step(ctx context.Context, p Stepper, user string, results []provider.Result, steer <-chan string, notice func(string)) ([]provider.Call, string, error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type reply struct {
		calls []provider.Call
		err   error
	}
	done := make(chan reply, 1)
	go func() {
		calls, err := p.Step(sctx, user, results)
		done <- reply{calls, err}
	}()
	for {
		select {
		case r := <-done:
			return r.calls, "", r.err
		case line, ok := <-steer:
			if !ok { // Ctrl-D: stop listening, let the reply finish
				steer = nil
				continue
			}
			if line == "" {
				continue
			}
			cancel()
			<-done // the provider drops its partial reply on cancel
			if notice != nil {
				notice("\n(steered)")
			}
			return nil, line, nil
		}
	}
}

// queued takes whatever was typed so far, without waiting.
func queued(msgs []string, steer <-chan string) string {
	for {
		select {
		case l, ok := <-steer:
			if !ok {
				return strings.Join(msgs, "\n")
			}
			if l != "" {
				msgs = append(msgs, l)
			}
		default:
			return strings.Join(msgs, "\n")
		}
	}
}

// runAll runs every call at once; results keep the model's order.
func runAll(ctx context.Context, ts []tools.Tool, calls []provider.Call) []provider.Result {
	byName := map[string]tools.Tool{}
	for _, t := range ts {
		byName[t.Name] = t
	}
	out := make([]provider.Result, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = provider.Result{ID: c.ID, Output: "unknown tool: " + c.Name, IsError: true}
			if t, ok := byName[c.Name]; ok {
				var result provider.Result
				var err error
				switch {
				case t.RunResult != nil:
					result, err = t.RunResult(ctx, c.Input)
				case t.Run != nil:
					result.Output, err = t.Run(ctx, c.Input)
				default:
					err = fmt.Errorf("tool %s has no implementation", c.Name)
				}
				if err != nil {
					result = provider.Result{Output: err.Error(), IsError: true}
				}
				if result.IsError {
					result.Images = nil
				}
				result.ID = c.ID
				out[i] = result
			}
		}()
	}
	wg.Wait()
	return out
}
