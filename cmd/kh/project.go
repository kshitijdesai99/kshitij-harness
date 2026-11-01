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

// Project MCP servers are external programs/services. Both connecting (which
// may launch a program) and calls use the active command approval policy.
func projectTools(ctx context.Context, project *projectconfig.Config, approval tools.Approver, policy *tools.ApprovalPolicy, notice func(string)) ([]tools.Tool, func(), []mcp.ConnectionResult, error) {
	if len(project.Servers) == 0 {
		return nil, func() {}, nil, nil
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
	var reports []mcp.ConnectionResult
	servers := make(map[string]mcp.Server)
	names := make([]string, 0, len(project.Servers))
	for name := range project.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	auto := policy.Auto()
	for _, name := range names {
		// Auto already grants startup trust. Avoid logging an approval action
		// for every server; OAuth consent and per-call approvals stay separate.
		allowed, err := auto, lifetime.Err()
		if !auto && err == nil {
			allowed, err = approval.Approve(lifetime, fmt.Sprintf("Trust MCP server %q: connect/start external code", name), false)
		}
		if err != nil || !allowed {
			reports = append(reports, mcp.ConnectionResult{Name: name, Status: "not connected"})
			notice(fmt.Sprintf("(project config) MCP server %q not connected", name))
			continue
		}
		s := project.Servers[name]
		servers[name] = mcp.Server{Command: s.Command, Args: s.Args, Env: s.Env, URL: s.URL, Headers: s.Headers, Type: s.Type, Dir: s.Dir}
	}
	ts, close, warnings := mcp.ConnectWithOptions(lifetime, servers, mcp.Options{OAuth: projectOAuth(approval, notice), Observe: func(report mcp.ConnectionResult) {
		reports = append(reports, report)
	}})
	for _, warning := range warnings {
		notice("(project config) " + warning)
	}
	for i := range ts {
		original := ts[i]
		ts[i].Run = nil
		ts[i].RunResult = func(ctx context.Context, input json.RawMessage) (provider.Result, error) {
			allowed, err := approval.Approve(ctx, fmt.Sprintf("Call MCP tool %q (external effects)", original.Name), policy.Auto())
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
	if auto {
		connected, failed, notConnected := 0, 0, 0
		for _, report := range reports {
			switch report.Status {
			case "connected":
				connected++
			case "failed":
				failed++
			default:
				notConnected++
			}
		}
		summary := fmt.Sprintf("(MCP: %d connected", connected)
		if failed > 0 {
			summary += fmt.Sprintf(", %d failed", failed)
		}
		if notConnected > 0 {
			summary += fmt.Sprintf(", %d not connected", notConnected)
		}
		notice(summary + ")")
	} else if len(ts) > 0 {
		notice(fmt.Sprintf("(project config) loaded %d MCP tools", len(ts)))
	}
	return ts, func() { cancel(); close() }, reports, lifetime.Err()
}
