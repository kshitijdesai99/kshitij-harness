package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"kh/internal/config"
	"kh/internal/ecosystem"
	"kh/internal/provider"
	"kh/internal/session"
	"kh/internal/tools"
)

// Runtime identity is independent of the private provider conversation. Only
// explicit task metadata and conclusions are visible through the registry.
type ecosystemRuntime struct {
	store         *ecosystem.Store
	self          ecosystem.Agent
	process       ecosystem.ProcessIdentity
	configuredMax int
}

var currentEcosystem *ecosystemRuntime

func ecosystemPane(ctx context.Context) ecosystem.PaneInfo {
	if os.Getenv("TMUX") == "" {
		return ecosystem.PaneInfo{}
	}
	pane, err := currentPane(ctx)
	if err != nil {
		return ecosystem.PaneInfo{}
	}
	sid, _ := tmuxContext(ctx, "display-message", "-p", "-t", pane, "#{session_id}")
	return ecosystem.PaneInfo{SessionID: sid, PaneID: pane}
}
func (e *ecosystemRuntime) reconcile(ctx context.Context) error {
	return e.store.Reconcile(ctx, e.self.EcosystemID, ecosystem.ProbeProcess)
}
func (e *ecosystemRuntime) snapshot(ctx context.Context) (string, error) {
	if err := e.reconcile(ctx); err != nil {
		return "", err
	}
	stats, err := e.store.Stats(ctx, e.self.EcosystemID)
	if err != nil {
		return "", err
	}
	self, err := e.store.Get(ctx, e.self.ID)
	if err != nil {
		return "", err
	}
	// Snapshots deliberately omit tasks, conclusions and process details;
	// explicit inspect loads shared details without bloating every tool result.
	b, err := json.Marshal(struct {
		Self  map[string]any  `json:"self"`
		Stats ecosystem.Stats `json:"stats"`
	}{map[string]any{"id": self.ID, "ecosystem_id": self.EcosystemID, "parent_id": self.ParentID,
		"address": self.Address, "name": self.Name, "role": self.Role, "state": self.State}, stats})
	return string(b), err
}
func (e *ecosystemRuntime) publishPane(ctx context.Context) error {
	if e.self.Pane.PaneID == "" {
		return nil
	}
	_, err := tmuxContext(ctx, "set-option", "-p", "-t", e.self.Pane.PaneID, "@kh_ecosystem", e.self.EcosystemID, ";",
		"set-option", "-p", "-t", e.self.Pane.PaneID, "@kh_agent_id", e.self.ID)
	return err
}
func (e *ecosystemRuntime) export() {
	os.Setenv("KH_ECOSYSTEM_ID", e.self.EcosystemID)
	os.Setenv("KH_ECOSYSTEM_AGENT_ID", e.self.ID)
	os.Unsetenv("KH_ECOSYSTEM_TOKEN")
}

// startEcosystem is the admission gate: no model or task runs before Claim.
// A redirected nested chat must not impersonate the pane's existing receiver.
func startEcosystem(ctx context.Context, cfg config.Config, conversation string, resuming bool) (*ecosystemRuntime, error) {
	if currentEcosystem != nil {
		return currentEcosystem, nil
	}
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			store.Close()
		}
	}()
	process, err := ecosystem.CurrentProcess()
	if err != nil {
		return nil, err
	}
	pane := ecosystemPane(ctx)
	if !chatTerminal() && os.Getenv("KH_ECOSYSTEM_TOKEN") == "" {
		pane = ecosystem.PaneInfo{}
	}
	e := &ecosystemRuntime{store: store, process: process}
	defer func() {
		if !ok && e.self.ID != "" {
			_ = store.PublishConclusion(context.Background(), e.self.ID, "startup failed before model execution")
			// The exiting process remains counted until an exact exit probe.
			// Do not stop surviving descendants of a failed root resume.
		}
	}()
	// Spawned chats were already claimed by claimInheritedStartup, before
	// rebuilding/configuration. Only independent roots reach this point.
	if os.Getenv("KH_ECOSYSTEM_AGENT_ID") != "" || os.Getenv("KH_ECOSYSTEM_ID") != "" {
		return nil, fmt.Errorf("scoped kh startup requires a spawn reservation; use native agents or /new for an independent user chat")
	}
	// Clearing inherited variables is not admission: an existing live pane
	// still owns its scope. /new and /fork create a genuinely fresh pane.
	if paneID, paneErr := currentPane(ctx); paneErr == nil {
		priorID, readErr := tmuxContext(ctx, "display-message", "-p", "-t", paneID, "#{@kh_agent_id}")
		if readErr != nil {
			return nil, readErr
		}
		if priorID != "" {
			prior, readErr := store.Get(ctx, priorID)
			if readErr != nil {
				return nil, readErr
			}
			identity := prior.Process
			if !identity.Valid() {
				identity = prior.Launcher
			}
			if identity.Valid() {
				alive, probeErr := ecosystem.ProbeProcess(identity)
				if probeErr != nil {
					return nil, probeErr
				}
				if alive {
					return nil, fmt.Errorf("pane already belongs to a live ecosystem; use native agents or /new")
				}
			}
		}
	}
	sidecar := filepath.Join(session.Folder(), "ecosystems", conversation+".json")
	var rootID string
	if resuming {
		data, readErr := os.ReadFile(sidecar)
		if readErr == nil {
			if err := json.Unmarshal(data, &rootID); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(readErr) {
			return nil, readErr
		}
	}
	if rootID != "" {
		old, err := store.Get(ctx, rootID)
		if err != nil {
			return nil, err
		}
		if old.ParentID != "" {
			return nil, fmt.Errorf("only root conversations can resume an ecosystem")
		}
		alive, probeErr := ecosystem.ProbeProcess(old.Process)
		if probeErr != nil {
			return nil, probeErr
		}
		if alive {
			return nil, fmt.Errorf("saved ecosystem root is still running")
		}
		if err := store.SetMax(ctx, old.ID, cfg.Agents.MaxLive); err != nil {
			return nil, err
		}
		if err := store.Reconcile(ctx, old.EcosystemID, ecosystem.ProbeProcess); err != nil {
			return nil, err
		}
		e.self, err = store.ResumeRoot(ctx, rootID, process, pane, ecosystem.ProbeProcess)
		if err != nil {
			return nil, err
		}
	} else {
		name := strings.TrimPrefix(os.Getenv("KH_AGENT"), "kh:")
		if !agentName.MatchString(name) {
			name = "main"
		}
		e.self, err = store.CreateRoot(ctx, ecosystem.RootOptions{Name: name, MaxAgents: cfg.Agents.MaxLive, Process: process, Pane: pane})
		if err != nil {
			return nil, err
		}
		if err := saveEcosystemIdentity(sidecar, e.self.ID); err != nil {
			return nil, fmt.Errorf("save session ecosystem identity: %w", err)
		}
	}
	if err := e.publishPane(ctx); err != nil {
		return nil, err
	}
	e.export()
	e.configuredMax = cfg.Agents.MaxLive
	currentEcosystem = e
	ok = true
	return e, nil
}

func saveEcosystemIdentity(path, id string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ecosystem-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	data, _ := json.Marshal(id)
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// CLI operations use the inherited scope, or the pane's published identity.
// Environment scope is authoritative: it never falls back to another root.
func operationEcosystem(ctx context.Context) (*ecosystemRuntime, func(), error) {
	if currentEcosystem != nil {
		return currentEcosystem, func() {}, nil
	}
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*ecosystemRuntime, func(), error) { store.Close(); return nil, nil, err }
	id, eco := os.Getenv("KH_ECOSYSTEM_AGENT_ID"), os.Getenv("KH_ECOSYSTEM_ID")
	if id == "" && eco == "" && os.Getenv("TMUX") != "" {
		pane, err := currentPane(ctx)
		if err != nil {
			return fail(err)
		}
		value, err := tmuxContext(ctx, "display-message", "-p", "-t", pane, "#{@kh_ecosystem}|#{@kh_agent_id}")
		if err != nil {
			return fail(err)
		}
		parts := strings.SplitN(value, "|", 2)
		if len(parts) == 2 {
			eco, id = parts[0], parts[1]
		}
	}
	if id == "" || eco == "" {
		return fail(fmt.Errorf("no active ecosystem in this pane; start a kh chat first"))
	}
	self, err := store.Get(ctx, id)
	if err != nil {
		return fail(err)
	}
	if self.EcosystemID != eco {
		return fail(fmt.Errorf("ecosystem scope mismatch"))
	}
	e := &ecosystemRuntime{store: store, self: self}
	if err := e.reconcile(ctx); err != nil {
		return fail(err)
	}
	return e, func() { store.Close() }, nil
}

func claimInheritedStartup(ctx context.Context) error {
	id, eco := os.Getenv("KH_ECOSYSTEM_AGENT_ID"), os.Getenv("KH_ECOSYSTEM_ID")
	if id == "" && eco == "" && os.Getenv("KH_ECOSYSTEM_TOKEN") == "" {
		return nil
	}
	if id == "" || eco == "" {
		return fmt.Errorf("incomplete ecosystem scope")
	}
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			store.Close()
		}
	}()
	process, err := ecosystem.CurrentProcess()
	if err != nil {
		return err
	}
	a, err := store.Get(ctx, id)
	if err != nil {
		return err
	}
	if a.EcosystemID != eco {
		return fmt.Errorf("ecosystem scope mismatch")
	}
	if token := os.Getenv("KH_ECOSYSTEM_TOKEN"); token != "" {
		a, err = store.Claim(ctx, id, token, process, ecosystemPane(ctx))
		if err != nil {
			return err
		}
	} else if a.Process != process || (a.State != ecosystem.Running && a.State != ecosystem.Idle) {
		// CLI operations are deliberately allowed to act in their inherited scope;
		// they do not run a model or start a new chat.
		return nil
	}
	e := &ecosystemRuntime{store: store, self: a, process: process}
	// Once claimed, retain the store for exit's durable startup diagnostic
	// even if publishing pane metadata fails before normal chat setup.
	currentEcosystem = e
	ok = true
	if err := e.publishPane(ctx); err != nil {
		return err
	}
	e.export()
	return nil
}

func (e *ecosystemRuntime) refreshLimit(ctx context.Context) error {
	if e.self.ParentID != "" {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Agents.MaxLive == e.configuredMax {
		return nil
	}
	if err := e.store.SetMax(ctx, e.self.ID, cfg.Agents.MaxLive); err != nil {
		return err
	}
	e.configuredMax = cfg.Agents.MaxLive
	return nil
}

func (e *ecosystemRuntime) decorateTools(ts []tools.Tool) []tools.Tool {
	for i := range ts {
		tool := ts[i]
		ts[i].Run = nil
		ts[i].RunResult = func(ctx context.Context, input json.RawMessage) (provider.Result, error) {
			var result provider.Result
			var err error
			if tool.RunResult != nil {
				result, err = tool.RunResult(ctx, input)
			} else {
				result.Output, err = tool.Run(ctx, input)
			}
			snapshot, snapshotErr := e.snapshot(ctx)
			if snapshotErr != nil {
				snapshot = "unavailable: " + snapshotErr.Error()
			}
			suffix := "\n\n[ecosystem snapshot] " + snapshot
			if err != nil {
				return result, fmt.Errorf("%w%s", err, suffix)
			}
			result.Output += suffix
			return result, nil
		}
	}
	return ts
}

func ecosystemInstructions(e *ecosystemRuntime) string {
	return "\n\nThis chat belongs to ecosystem " + e.self.EcosystemID + ". Its address is " + e.self.Address + " (alias kh:" + e.self.Name + "). The root is always manager. Descendants start as workers and may use agents set_role to become manager or worker at any time. Managers delegate; workers execute assigned work and can become managers before recursively spawning. Use native agents for orchestration; all participants may discover, message, safely inspect or stop any member in this ecosystem. Share conclusions and statistics, not conversation histories. Capacity is ecosystem-wide; at capacity retain orchestration and wait, inspect or stop rather than bypassing admission."
}

// Stop releases a slot only after exact process identity is confirmed absent.
// A self-stop runs last and exits after the tool result has been returned.
func (e *ecosystemRuntime) stop(ctx context.Context, address, scope string) (string, error) {
	target, err := e.store.Resolve(ctx, e.self.EcosystemID, address)
	if err != nil {
		return "", err
	}
	targets, err := e.store.Stop(ctx, e.self.ID, target.ID, scope == "subtree")
	if err != nil {
		return "", err
	}
	// Once the registry transition commits, steering/cancellation cannot leave
	// half a subtree marked stopping but never signalled. Signal everyone first,
	// then bound the shared wait (not one timeout per agent).
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer cancel()
	var errs []error
	selfStop := false
	pending := make(map[string]ecosystem.ProcessIdentity)
	for _, a := range targets {
		process := a.Process
		if !process.Valid() {
			// Before observation Launcher can be the parent. A recorded pane
			// instead identifies the child which is fenced from late Claim.
			if a.Pane.PaneID == "" {
				continue
			}
			process = a.Launcher
		}
		if !process.Valid() {
			continue
		}
		if process.PID == os.Getpid() {
			selfStop = true
			continue
		}
		alive, probeErr := ecosystem.ProbeProcess(process)
		if probeErr != nil {
			errs = append(errs, probeErr)
			continue
		}
		if !alive {
			continue
		}
		if signalErr := syscall.Kill(process.PID, syscall.SIGTERM); signalErr != nil && signalErr != syscall.ESRCH {
			errs = append(errs, signalErr)
			continue
		}
		pending[a.Address] = process
	}
	for attempt := 0; len(pending) > 0 && attempt < 40; attempt++ {
		for address, process := range pending {
			alive, probeErr := ecosystem.ProbeProcess(process)
			if probeErr != nil {
				errs = append(errs, probeErr)
				delete(pending, address)
				continue
			}
			if !alive {
				delete(pending, address)
				continue
			}
			if attempt == 20 {
				_ = syscall.Kill(process.PID, syscall.SIGKILL)
			}
		}
		if len(pending) == 0 {
			break
		}
		select {
		case <-cleanup.Done():
			attempt = 40
		case <-time.After(50 * time.Millisecond):
		}
	}
	for address := range pending {
		errs = append(errs, fmt.Errorf("%s exit not confirmed", address))
	}
	// Reconcile probes exact incarnations again before releasing any slot.
	errs = append(errs, e.reconcile(cleanup))
	if selfStop {
		go func() { time.Sleep(150 * time.Millisecond); _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) }()
	}
	return fmt.Sprintf("stop requested for %d agent(s)", len(targets)), errors.Join(errs...)
}

func (e *ecosystemRuntime) conclusion(model provider.Provider, err error) {
	// Root replies belong to the private user conversation, not to a delegated
	// task. Promoted descendants still publish their bounded task results.
	if e.self.ParentID == "" {
		return
	}
	text := ""
	if err != nil {
		// A failed step may leave an older assistant reply in provider-owned
		// history. Never mistake that private prior reply for this task's result.
		text = "Failed: " + err.Error()
	} else if r, ok := model.(provider.LastResponder); ok {
		text = r.LastResponse()
	}
	if len(text) > 16000 {
		text = strings.ToValidUTF8(text[:16000], "") + "…"
	}
	if text != "" {
		_ = e.store.PublishConclusion(context.Background(), e.self.ID, text)
	}
}
