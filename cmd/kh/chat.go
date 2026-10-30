package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"
	"unicode/utf8"

	"kh/internal/agent"
	"kh/internal/backend"
	"kh/internal/config"
	"kh/internal/learn"
	"kh/internal/memory"
	"kh/internal/projectconfig"
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
	identity          *session.Identity
	memory            *memory.Store
	memoryActive      bool // applied pipeline, distinct from persisted opt-in after startup failure
	budget            memory.Budget
	learn             *learn.Learner // nil when automatic memory is off
	input             *terminal.Console
	ui                terminal.Renderer
	tools             []tools.Tool
	seen              config.Config
	recentContext     string // bounded last exchange; never a second transcript archive
	policy            *tools.ApprovalPolicy
	project           *projectconfig.Config
	forkMu            sync.Mutex
	forkState         []byte // last completed conversation; never read a streaming model
	forkModel         string
	forkEffort        string

	lastMemoryDiagnostic string // last retrieval only; no query or note bodies
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
	if now, err := config.Load(); err == nil {
		if now.Provider == c.backend {
			if now.Model != c.seen.Model {
				c.model.Use(now.Model, "")
			}
			if now.Effort != c.seen.Effort {
				c.model.Use("", now.Effort)
			}
			c.seen.Model, c.seen.Effort = now.Model, now.Effort
		}
		if now.Auto != c.seen.Auto {
			applyAuto(c.policy, now.Auto)
		}
		c.seen.Auto = now.Auto
	}
	exportModel(c.model, c.backend)
	if err := c.snapshotFork(); err != nil {
		return err
	}
	tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	start := time.Now()
	c.showMemory()
	recent := c.recentContext
	if recent == "" {
		if r, ok := c.model.(provider.LastResponder); ok {
			recent = memoryContext(r.LastResponse())
		}
	}
	currentQuery := msg
	var origin learn.RunOrigin
	beforeMessage := func(ctx context.Context, query string) error {
		origin.Observe(query)
		// Fence immediately, even if this run is later cancelled or fails.
		recent = origin.Context(recent)
		c.recentContext = origin.Context(c.recentContext)
		c.reloadMemory(ctx)
		currentQuery = query
		if c.learn != nil {
			c.learn.MessageAt(query, recent, time.Now())
		}
		if setter, ok := c.model.(provider.MemorySetter); ok {
			// Includes steering and queued queries, not only the initial prompt.
			state, err := c.model.Save()
			if err != nil {
				return fmt.Errorf("retrieve memory: %w", err)
			}
			retrievalStart := time.Now()
			relevant, trace, err := c.memory.RecallWithTrace(ctx, c.repo, query, memory.Seen(state), c.budget)
			elapsed := time.Since(retrievalStart).Round(time.Millisecond)
			c.lastMemoryDiagnostic = fmt.Sprintf("%s (%s)", trace.Diagnostic(err), elapsed)
			c.ui.Notice(fmt.Sprintf("%s (%s)", trace.Summary(err), elapsed))
			if err != nil {
				return fmt.Errorf("retrieve memory: %w", err)
			}
			setter.SetMemory(relevant)
		}
		return nil
	}
	var steps []learn.Step
	runner := agent.Runner{Model: c.model, Tools: c.tools, Input: c.input.Lines,
		Held: c.input.Held, Notice: c.ui.Notice, BeforeMessage: beforeMessage, ExpandMessage: c.project.Expand,
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
	if err == nil && tctx.Err() == nil {
		reply := ""
		if r, ok := c.model.(provider.LastResponder); ok {
			reply = r.LastResponse()
		}
		// Tools and replies span message boundaries. A clean follow-up cannot
		// relabel evidence from a peer-coordinated run for durable learning.
		if origin.Eligible() {
			if c.learn != nil {
				c.learn.FinishedAt(currentQuery, recent, steps, reply, time.Now())
			}
			c.recentContext = memoryContext("User: "+currentQuery) + "\n" + memoryContext("Assistant: "+reply)
		}
	}
	c.showMemory()
	if s := c.model.Stats(); s.In > 0 {
		c.ui.Statistics(fmt.Sprintf("(ttft %s, total %s, %s in, %s cached %d%%, %s out, %s thinking, %s)",
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
		c.ui.Notice(terminal.Color(c.ui.Err, terminal.Muted, "(memory) "+n))
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
		b, saveErr = session.EncodeWithIdentity(c.backend, b, c.identity)
	}
	if saveErr == nil {
		saveErr = session.Save(c.id, b)
	}
	if saveErr != nil {
		return fmt.Errorf("save session %s: %w", c.id, saveErr)
	}
	return c.snapshotFork()
}

// snapshotFork runs only while the provider is idle. Input can fork this
// immutable checkpoint without racing a reply or copying unpaired tool calls.
func (c *chat) snapshotFork() error {
	state, err := c.model.Save()
	if err != nil {
		return err
	}
	state, err = session.EncodeWithIdentity(c.backend, state, c.identity)
	if err != nil {
		return err
	}
	if r, ok := c.model.(provider.LastResponder); ok && r.LastResponse() == "" {
		state = nil
	}
	model, effort := c.model.Use("", "")
	c.forkMu.Lock()
	c.forkState, c.forkModel, c.forkEffort = state, model, effort
	c.forkMu.Unlock()
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
	started := time.Now()
	if c.ui.Activity != nil {
		c.ui.Activity.Start("compacting context")
		defer c.ui.Activity.Stop()
	}
	c.ui.Notice("(compacting context... Ctrl-C to cancel)")
	if err := compacter.Compact(tctx); err != nil {
		if errors.Is(err, context.Canceled) || tctx.Err() == context.Canceled {
			c.ui.Notice("(compaction cancelled; conversation unchanged)")
			return nil
		}
		return fmt.Errorf("failed; conversation unchanged: %w", err)
	}
	if c.ui.Activity != nil {
		c.ui.Activity.Set("saving compacted context")
	}
	if err := c.save(); err != nil {
		return fmt.Errorf("context compacted, but session/checkpoint save failed: %w", err)
	}
	c.ui.Notice(fmt.Sprintf("(context compacted and session saved in %s)", secs(time.Since(started))))
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

// memoryContext keeps recent evidence bounded without cutting a UTF-8 rune.
func memoryContext(text string) string {
	if len(text) <= 1800 {
		return text
	}
	n := 1800
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n] + "…"
}

func secs(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
