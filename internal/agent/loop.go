// Package agent is the loop: ask the model, run its tools, repeat.
package agent

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"kh/internal/image"
	"kh/internal/provider"
	"kh/internal/tools"
)

// Run does one task, and lets the user steer it by typing a line on steer:
//   - while the model replies, the reply is cut off and restarted with the line;
//   - while commands run, the line is sent with their results (cutting a
//     command off halfway could leave things broken).
func Run(ctx context.Context, p provider.Provider, ts []tools.Tool, task string, steer <-chan string) error {
	user, results := task, []provider.Result(nil)
	for {
		var err error
		user, err = withPastedImage(p, user)
		if err != nil {
			return err
		}
		calls, line, err := step(ctx, p, user, results, steer)
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
		results = runAll(ctx, ts, calls)
		user = queued(tools.Held(), steer)
	}
}

var pastedImage = regexp.MustCompile(`\[\[kh:image:([a-f0-9]{32})\]\]`)

func withPastedImage(p provider.Provider, text string) (string, error) {
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
	url, err := image.Take(matches[0][1])
	if err != nil {
		return "", err
	}
	attacher, ok := p.(interface{ AttachImage(string) })
	if !ok {
		return "", fmt.Errorf("this provider does not support images")
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
func step(ctx context.Context, p provider.Provider, user string, results []provider.Result, steer <-chan string) ([]provider.Call, string, error) {
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
			fmt.Fprintln(os.Stderr, "\n(steered)")
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
