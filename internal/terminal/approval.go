package terminal

import (
	"context"
	"fmt"
	"strings"

	"kh/internal/tools"
)

// Approve serializes parallel questions, but both waiting for the prompt and
// waiting for its answer remain cancellable.
func (c *Console) Approve(ctx context.Context, command string, safe bool) (bool, error) {
	if err := c.lockApproval(ctx); err != nil {
		return false, err
	}
	defer func() { c.approval <- struct{}{} }()
	if !safe {
		c.activityPhase("waiting for approval")
		defer c.activityPhase("running bash")
	}
	(Renderer{Err: c.Stderr()}).Action(tools.CommandLabel(command))
	if safe {
		return true, nil
	}
	return c.answerApproval(ctx, "  run it? [y/N] ")
}

func (c *Console) approvalError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return fmt.Errorf("console closed")
	default:
		return nil
	}
}

func (c *Console) lockApproval(ctx context.Context) error {
	if err := c.approvalError(ctx); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("console closed")
	case <-c.approval:
	}
	if err := c.approvalError(ctx); err != nil {
		c.approval <- struct{}{}
		return err
	}
	return nil
}

func (c *Console) answerApproval(ctx context.Context, prompt string) (bool, error) {
	for {
		if err := c.approvalError(ctx); err != nil {
			return false, err
		}
		fmt.Fprint(c.Stderr(), prompt)
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-c.done:
			return false, fmt.Errorf("console closed")
		case line, ok := <-c.lines:
			if !ok {
				return false, c.approvalError(ctx)
			}
			switch strings.ToLower(line) {
			case "y", "yes":
				if err := c.approvalError(ctx); err != nil {
					return false, err
				}
				return true, nil
			case "", "n", "no":
				return false, c.approvalError(ctx)
			default:
				c.mu.Lock()
				c.pending = append(c.pending, line)
				c.mu.Unlock()
				fmt.Fprintln(c.Stderr(), "  (noted for the model)")
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
