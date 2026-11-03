package codex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"kh/internal/provider"
)

func (c *Client) SetEventObserver(observe func(provider.Event) error) { c.observe = observe }
func eventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func (c *Client) event(ev provider.Event) error {
	if c.observe == nil {
		return nil
	}
	if c.observeErr != nil {
		return c.observeErr
	}
	ev.AttemptID = c.attemptID
	if err := c.observe(ev); err != nil {
		c.observeErr = fmt.Errorf("record conversation: %w", err)
	}
	return c.observeErr
}

func (c *Client) startAttempt() error {
	c.attemptID = eventID()
	return c.event(provider.Event{Type: "attempt_start"})
}
func (c *Client) endAttempt(err error) error {
	status := "completed"
	if err != nil {
		status = "failed"
	}
	if errors.Is(err, context.Canceled) {
		status = "interrupted"
	}
	return errors.Join(err, c.event(provider.Event{Type: "attempt_end", Status: status}))
}
