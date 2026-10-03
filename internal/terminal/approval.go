package terminal

import (
	"context"
	"fmt"
	"strings"
)

// Approve serializes parallel questions, but both waiting for the prompt and
// waiting for its answer remain cancellable.
func (c *Console) Approve(ctx context.Context, command string, safe bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-c.done:
		return false, fmt.Errorf("console closed")
	case <-c.approval:
	}
	defer func() { c.approval <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !safe {
		c.activityPhase("waiting for approval")
		defer c.activityPhase("running bash")
	}
	fmt.Fprintln(c.err, "$", command)
	if safe {
		return true, nil
	}
	for {
		fmt.Fprint(c.err, "  run it? [y/N] ")
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-c.done:
			return false, fmt.Errorf("console closed")
		case line, ok := <-c.lines:
			if !ok {
				return false, nil
			}
			switch strings.ToLower(line) {
			case "y", "yes":
				return true, nil
			case "", "n", "no":
				return false, nil
			default:
				c.mu.Lock()
				c.pending = append(c.pending, line)
				c.mu.Unlock()
				fmt.Fprintln(c.err, "  (noted for the model)")
			}
		}
	}
}

func (c *Console) Held() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := c.pending
	c.pending = nil
	return lines
}
