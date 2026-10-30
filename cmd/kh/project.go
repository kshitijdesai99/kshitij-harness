package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"

	"kh/internal/mcp"
	"kh/internal/projectconfig"
	"kh/internal/provider"
	"kh/internal/tools"
)

// Project MCP servers are external programs/services, not sandboxed shell tools.
// Both connecting (which may launch a program) and calls use the active approval
// policy. Configuration never changes kh's own permissions or folder grants.
func projectTools(ctx context.Context, project *projectconfig.Config, approval tools.Approver, access *tools.WriteAccess, notice func(string)) ([]tools.Tool, func(), error) {
	if len(project.Servers) == 0 {
		return nil, func() {}, nil
	}
	lifetime, cancel := context.WithCancel(ctx)
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	ready := make(chan struct{})
	go func() {
		select {
		case <-interrupts:
			cancel()
		case <-ready:
		case <-lifetime.Done():
		}
	}()
	defer func() { signal.Stop(interrupts); close(ready) }()
	servers := make(map[string]mcp.Server)
	names := make([]string, 0, len(project.Servers))
	for name := range project.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		allowed, err := approval.Approve(lifetime, fmt.Sprintf("Trust MCP server %q: connect/start external code; effects are outside kh's bash sandbox", name), access.Auto())
		if err != nil || !allowed {
			notice(fmt.Sprintf("(project config) MCP server %q not connected", name))
			continue
		}
		s := project.Servers[name]
		servers[name] = mcp.Server{Command: s.Command, Args: s.Args, Env: s.Env, URL: s.URL, Headers: s.Headers, Type: s.Type, Dir: s.Dir}
	}
	ts, close, warnings := mcp.Connect(lifetime, servers)
	for _, warning := range warnings {
		notice("(project config) " + warning)
	}
	for i := range ts {
		original := ts[i]
		ts[i].Run = nil
		ts[i].RunResult = func(ctx context.Context, input json.RawMessage) (provider.Result, error) {
			allowed, err := approval.Approve(ctx, fmt.Sprintf("Call MCP tool %q (external effects are not bash-sandboxed)", original.Name), access.Auto())
			if err != nil {
				return provider.Result{}, err
			}
			if !allowed {
				return provider.Result{}, fmt.Errorf("user said no")
			}
			if err := ctx.Err(); err != nil {
				return provider.Result{}, err
			}
			if original.RunResult != nil {
				return original.RunResult(ctx, input)
			}
			output, err := original.Run(ctx, input)
			return provider.Result{Output: output}, err
		}
	}
	if len(ts) > 0 {
		notice(fmt.Sprintf("(project config) loaded %d MCP tools", len(ts)))
	}
	return ts, func() { cancel(); close() }, lifetime.Err()
}
