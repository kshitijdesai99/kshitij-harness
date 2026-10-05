package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"kh/internal/agent"
	"kh/internal/backend"
	"kh/internal/config"
	"kh/internal/learn"
	"kh/internal/memory"
	"kh/internal/provider"
	"kh/internal/session"
	"kh/internal/terminal"
	"kh/internal/tools"
)

// chat owns the application lifecycle for one session. Its model is an
// interface; adapter selection and CLI flags stay in main.
type chat struct {
	model             provider.Provider
	backend, id, repo string
	memory            *memory.Store
	budget            memory.Budget
	learn             *learn.Learner // nil when automatic memory is off
	input             *terminal.Console
	ui                terminal.Renderer
	tools             []tools.Tool
	seen              config.Config
}

func (c *chat) turn(ctx context.Context, msg string) error {
	if c.ui.Activity != nil {
		c.ui.Activity.Start("working")
		defer c.ui.Activity.Stop()
	}
	setAgentState("busy")
	defer setAgentState("waiting")
	// Flags hold until the file changes. A different backend starts a new
	// chat rather than switching the interpretation of an existing history.
	if now, err := config.Load(); err == nil && now.Provider == c.backend {
		if now.Model != c.seen.Model {
			c.model.Use(now.Model, "")
		}
		if now.Effort != c.seen.Effort {
			c.model.Use("", now.Effort)
		}
		c.seen.Model, c.seen.Effort = now.Model, now.Effort
	}
	exportModel(c.model, c.backend)
	tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	start := time.Now()
	c.showMemory()
	if c.learn != nil {
		c.learn.Message(msg)
	}
	if setter, ok := c.model.(provider.MemorySetter); ok {
		// The saved chat says which notes it already holds; send only the rest.
		state, err := c.model.Save()
		if err != nil {
			return fmt.Errorf("retrieve memory: %w", err)
		}
		relevant, err := c.memory.Recall(tctx, c.repo, msg, memory.Seen(state), c.budget)
		if err != nil {
			return fmt.Errorf("retrieve memory: %w", err)
		}
		setter.SetMemory(relevant)
	}
	var steps []learn.Step
	runner := agent.Runner{Model: c.model, Tools: c.tools, Input: c.input.Lines,
		Held: c.input.Held, Notice: c.ui.Notice,
		Observe: func(calls []provider.Call, results []provider.Result) {
			for i := range min(len(calls), len(results)) {
				steps = append(steps, learn.Step{Name: calls[i].Name, Input: string(calls[i].Input),
					Output: results[i].Output, Failed: learn.Failed(results[i].IsError, results[i].Output)})
			}
		}}
	if c.ui.Activity != nil {
		runner.Activity = c.ui.Activity.Set
	}
	err := runner.Run(tctx, msg)
	if tctx.Err() == context.Canceled {
		c.ui.Notice("\n(stopped)")
		err = nil
	}
	if c.learn != nil && err == nil {
		reply := ""
		if r, ok := c.model.(provider.LastResponder); ok {
			reply = r.LastResponse()
		}
		c.learn.Finished(steps, reply)
	}
	c.showMemory()
	if s := c.model.Stats(); s.In > 0 {
		c.ui.Notice(fmt.Sprintf("(ttft %s, total %s, %s in, %s cached %d%%, %s out, %s thinking, %s)",
			secs(s.TTFT), secs(time.Since(start)), k(s.In), k(s.Cached), s.Cached*100/s.In, k(s.Out), k(s.Think), contextUsed(s)))
	}
	if c.ui.Activity != nil {
		c.ui.Activity.Set("saving session")
	}
	return errors.Join(err, c.save())
}

// showMemory reports what the background hooks saved, between turns so it
// never interleaves with a streaming reply.
func (c *chat) showMemory() {
	if c.learn == nil {
		return
	}
	for _, n := range c.learn.Notices() {
		c.ui.Notice(terminal.Color(os.Stdout, terminal.Muted, "(memory) "+n))
	}
}

// hookModel makes the cheap, tool-less provider the memory hooks use.
func hookModel(cfg config.Config) learn.Model {
	return func(system string, out provider.Output) (provider.Provider, error) {
		c := cfg
		c.System, c.WebSearch, c.Effort = system, false, "low"
		if cfg.MemoryModel != "" {
			c.Model = cfg.MemoryModel
		}
		return backend.New(c, "", nil, out)
	}
}

func (c *chat) save() error {
	b, saveErr := c.model.Save()
	if saveErr == nil {
		b, saveErr = session.Encode(c.backend, b)
	}
	if saveErr == nil {
		saveErr = session.Save(c.id, b)
	}
	if saveErr != nil {
		return fmt.Errorf("save session %s: %w", c.id, saveErr)
	}
	return nil
}

func (c *chat) compact(ctx context.Context) error {
	compacter, ok := c.model.(provider.Compacter)
	if !ok {
		return fmt.Errorf("this provider does not support compaction")
	}
	tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	setAgentState("busy")
	defer setAgentState("waiting")
	if c.ui.Activity != nil {
		c.ui.Activity.Start("compacting context")
		defer c.ui.Activity.Stop()
	}
	if err := compacter.Compact(tctx); err != nil {
		return err
	}
	if err := c.save(); err != nil {
		return err
	}
	c.ui.Notice("(context compacted and session saved)")
	return nil
}

// Child agents inherit the active selection, including command-line model
// overrides, instead of silently falling back to the config file's backend.
func exportModel(p provider.Provider, backend string) {
	model, effort := p.Use("", "")
	os.Setenv("KH_PROVIDER", backend)
	os.Setenv("KH_MODEL", model)
	os.Setenv("KH_EFFORT", effort)
}

func k(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func contextUsed(s provider.Stats) string {
	if s.Window == 0 {
		return "context " + k(s.Context)
	}
	return fmt.Sprintf("context %s/%s %d%%", k(s.Context), k(s.Window), s.Context*100/s.Window)
}

func secs(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
