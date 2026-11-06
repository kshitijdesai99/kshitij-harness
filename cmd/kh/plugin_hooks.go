package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"kh/internal/pluginhooks"
	"kh/internal/projectconfig"
	"kh/internal/session"
)

// pluginLifecycle exists only for root chats with explicitly trusted hooks.
// Projection is intentionally separate from the full provider archive.
type pluginLifecycle struct {
	project    *projectconfig.Config
	input      pluginhooks.Input
	runtime    pluginhooks.Runner
	trusted    []projectconfig.Hook
	transcript *pluginhooks.Transcript
	notice     func(string)
	pending    string
	endOnce    sync.Once
}

var activePluginHooks *pluginLifecycle

func (h *pluginLifecycle) end() {
	if h == nil {
		return
	}
	h.endOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.run(ctx, "SessionEnd", "", "", nil, false)
	})
}

func newPluginLifecycle(project *projectconfig.Config, cwd, id string, root bool, notice func(string)) (*pluginLifecycle, error) {
	if !root || project == nil || len(project.Hooks) == 0 {
		return nil, nil
	}
	path, err := projectconfig.HookTrustPath()
	if err != nil {
		return nil, err
	}
	grants, err := projectconfig.LoadHookTrust(path)
	if err != nil {
		return nil, err
	}
	var trusted []projectconfig.Hook
	for _, h := range project.Hooks {
		if grants[h.TrustID] {
			trusted = append(trusted, h)
		}
	}
	if len(trusted) == 0 {
		return nil, nil
	}
	transcript, err := pluginhooks.OpenTranscript("", cwd, id)
	if err != nil {
		return nil, err
	}
	return &pluginLifecycle{project: project, trusted: trusted, transcript: transcript, notice: notice, input: pluginhooks.Input{Cwd: cwd, SessionID: id, TranscriptPath: transcript.Path(), Source: "startup"}}, nil
}

func (h *pluginLifecycle) run(ctx context.Context, event, prompt, toolName string, input json.RawMessage, active bool) string {
	if h == nil || ctx.Err() != nil {
		return ""
	}
	in := h.input
	in.Event = event
	in.Prompt = prompt
	in.ToolName = toolName
	in.ToolInput = input
	in.StopHookActive = active
	result := h.runtime.Run(ctx, h.trusted, in)
	for _, line := range result.Diagnostics {
		if h.notice != nil {
			h.notice("(plugin hook) " + line)
		}
	}
	return strings.Join(append(result.SystemMessage, result.AdditionalContext...), "\n\n")
}

func (h *pluginLifecycle) user(ctx context.Context, text string) error {
	if h == nil {
		return nil
	}
	if err := h.transcript.AppendUser(session.New(), text, time.Now()); err != nil {
		return err
	}
	extra := h.run(ctx, "UserPromptSubmit", text, "", nil, false)
	h.pending = strings.TrimSpace(h.pending + "\n\n" + extra)
	return ctx.Err()
}

func pluginCommand(ctx context.Context, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	foundation, err := projectconfig.Resolve(ctx, cwd)
	if err != nil {
		return err
	}
	path, err := projectconfig.HookTrustPath()
	if err != nil {
		return err
	}
	grants, err := projectconfig.LoadHookTrust(path)
	if err != nil {
		return err
	}
	if len(args) == 0 || (len(args) == 1 && args[0] == "list") {
		seen := map[string]bool{}
		for _, h := range foundation.Config.Hooks {
			if seen[h.TrustID] {
				continue
			}
			seen[h.TrustID] = true
			fmt.Printf("%s\t%s\ttrusted=%t\t%s\n", h.TrustID, h.Plugin, grants[h.TrustID], h.PluginRoot)
		}
		return nil
	}
	if len(args) != 2 || (args[0] != "trust" && args[0] != "revoke") {
		return fmt.Errorf("usage: kh plugins [list|trust TRUST_ID|revoke TRUST_ID]")
	}
	if args[0] == "trust" {
		found := false
		for _, h := range foundation.Config.Hooks {
			found = found || h.TrustID == args[1]
		}
		if !found {
			return fmt.Errorf("unknown current plugin hook trust identity; run kh plugins list")
		}
	}
	if err := projectconfig.SetHookTrust(path, args[1], args[0] == "trust"); err != nil {
		return err
	}
	fmt.Printf("Plugin hook trust %s: %s\n", args[0], args[1])
	return nil
}
