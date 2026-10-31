package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"kh/internal/config"
	"kh/internal/ecosystem"
)

type spawnReservationKey struct{}
type spawnAttemptKey struct{}
type spawnAttempt struct{ attempted, created bool }

func agentOperation(ctx context.Context, args []string, cfg config.Config) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("need agent command")
	}
	e, close, err := operationEcosystem(ctx)
	if err != nil {
		return "", err
	}
	defer close()
	switch args[0] {
	case "stats":
		if len(args) != 1 {
			return "", fmt.Errorf("usage: kh stats")
		}
		return e.snapshot(ctx)
	case "inspect", "peek":
		if len(args) != 2 || !validAddress(args[1]) {
			return "", fmt.Errorf("usage: kh %s kh:<address>", args[0])
		}
		a, err := e.store.Resolve(ctx, e.self.EcosystemID, args[1])
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(a)
		return string(b), err
	case "agents":
		if len(args) != 1 {
			return "", fmt.Errorf("usage: kh agents")
		}
		agents, err := e.store.List(ctx, e.self.EcosystemID)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		for _, a := range agents {
			fmt.Fprintf(&out, "kh:%s  %s  %s  %s parent=%s\n", a.Name, a.State, a.Role, a.Address, a.ParentID)
		}
		snapshot, err := e.snapshot(ctx)
		return strings.TrimSpace(out.String()) + "\n" + snapshot, err
	case "set_role":
		if len(args) != 2 {
			return "", fmt.Errorf("usage: kh set_role manager|worker")
		}
		if err := e.store.SetRole(ctx, e.self.ID, ecosystem.Role(args[1])); err != nil {
			return "", err
		}
		return e.snapshot(ctx)
	case "stop":
		if len(args) < 2 || len(args) > 3 || !validAddress(args[1]) {
			return "", fmt.Errorf("usage: kh stop kh:<address> [agent|subtree]")
		}
		scope := "agent"
		if len(args) == 3 && args[2] != "" {
			scope = args[2]
		}
		if scope != "agent" && scope != "subtree" {
			return "", fmt.Errorf("stop scope must be agent or subtree")
		}
		return e.stop(ctx, args[1], scope)
	case "send":
		return e.agentTransport(ctx, args, cfg)
	case "spawn":
		spawnArgs := args[1:]
		if len(spawnArgs) > 0 && spawnArgs[0] == "--keep" {
			spawnArgs = spawnArgs[1:]
		}
		if len(spawnArgs) != 2 || !agentName.MatchString(spawnArgs[0]) || spawnArgs[0] == "main" || strings.TrimSpace(spawnArgs[1]) == "" {
			return "", fmt.Errorf("usage: kh spawn [--keep] <name> <task> (name cannot be main)")
		}
		// Every path, including a CLI spawned by bash, uses this shared transaction.
		reservation, err := e.store.Reserve(ctx, e.self.ID, spawnArgs[0], spawnArgs[1])
		if err != nil {
			return "", err
		}
		launcher, err := ecosystem.CurrentProcess()
		if err != nil {
			_ = e.store.CancelReservation(context.Background(), reservation.Agent.ID, reservation.Token)
			return "", err
		}
		if err := e.store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, launcher); err != nil {
			_ = e.store.CancelReservation(context.Background(), reservation.Agent.ID, reservation.Token)
			return "", err
		}
		attempt := &spawnAttempt{}
		ctx = context.WithValue(ctx, spawnReservationKey{}, reservation)
		ctx = context.WithValue(ctx, spawnAttemptKey{}, attempt)
		out, err := e.agentTransport(ctx, args, cfg)
		if err != nil {
			// Pre-launch failures prove no child. A nonzero tmux server response from
			// our single split command also proves no pane, but cancellation does not.
			if !attempt.attempted || (!attempt.created && ctx.Err() == nil) {
				_ = e.store.FailLaunch(context.Background(), reservation.Agent.ID, reservation.Token, true)
			}
			return out, err
		}
		return out + "; address " + reservation.Agent.Address, nil
	}
	return "", fmt.Errorf("unknown agent command: %s", args[0])
}

// read-only shell helpers must use the active registry address even when the
// user moves panes between windows or sessions.
func registryAddress() (string, bool) {
	if currentEcosystem != nil {
		return currentEcosystem.self.Address, true
	}
	id := os.Getenv("KH_ECOSYSTEM_AGENT_ID")
	if id == "" {
		return "", false
	}
	return "kh:" + id, true
}
