package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/delegation"
	"kh/internal/ecosystem"
	"kh/internal/visual"
)

func TestTaskWorkerVisualUsesRootOwner(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		name := "structured"
		if reuse {
			name = "reusable"
		}
		t.Run(name, func(t *testing.T) {
			binary, _ := newEcosystemProviderRequests(t, func(step int) *projectCLICall {
				if step == 0 {
					return &projectCLICall{Name: "tool_invoke", Arguments: `{"name":"visual","arguments":{"action":"create","name":"worker-notebook"}}`}
				}
				return nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			store, err := ecosystem.Open(ecosystem.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			process, err := ecosystem.CurrentProcess()
			if err != nil {
				t.Fatal(err)
			}
			root, err := store.CreateRoot(ctx, ecosystem.RootOptions{Name: "visual-manager", MaxAgents: 3, Process: process})
			if err != nil {
				t.Fatal(err)
			}
			manager, err := visual.New(visual.Options{Owner: root.Process})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			reservation, err := store.Reserve(ctx, root.ID, "visual-worker", "create shared notebook")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, process); err != nil {
				t.Fatal(err)
			}
			directory, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			directory, err = filepath.EvalSymlinks(directory)
			if err != nil {
				t.Fatal(err)
			}
			h := delegation.Handoff{Version: 1, Task: "create shared notebook", Directory: directory}
			args := []string{"--task-handoff", warmHandoffJSON(t, h)}
			if reuse {
				args = append(args, "--reuse-worker")
			}
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Stdin = strings.NewReader("")
			cmd.Env = append(os.Environ(), "KH_ECOSYSTEM_ID="+root.EcosystemID, "KH_ECOSYSTEM_AGENT_ID="+reservation.Agent.ID, "KH_ECOSYSTEM_TOKEN="+reservation.Token, "KH_PARENT="+root.Address)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "PROJECT-CLI-DONE") {
				t.Fatalf("worker: %s: %v", out, err)
			}
			// Exercise the actual worker tool registry and deferred Close, not
			// only the visual package's explicitly configured shared clients.
			if _, err := os.Stat(filepath.Join(manager.Root(), "worker-notebook.py")); err != nil {
				t.Fatalf("worker notebook not in root workspace: %v; output=%s", err, out)
			}
			if _, err := os.Stat(filepath.Join(manager.Root(), "state", "closed")); !os.IsNotExist(err) {
				t.Fatalf("worker completion closed root visuals: %v", err)
			}
		})
	}
}
