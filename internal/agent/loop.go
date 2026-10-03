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
}

// Run executes one task. Steering interrupts a reply, but lets running tools
// finish so their results can still be paired with the calls in history.
func (r Runner) Run(ctx context.Context, task string) error {
	p, ts, steer := r.Model, r.Tools, r.Input
	user, results := task, []provider.Result(nil)
	for {
		var err error
		user, err = withPastedImage(p, user)
		if err != nil {
			return err
		}
		if r.Activity != nil {
			r.Activity("waiting for model")
		}
		calls, line, err := step(ctx, p, user, results, steer, r.Notice)
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
				s, err := t.Run(ctx, c.Input)
				if err != nil {
					s = err.Error()
				}
				out[i] = provider.Result{ID: c.ID, Output: s, IsError: err != nil}
			}
		}()
	}
	wg.Wait()
	return out
}
