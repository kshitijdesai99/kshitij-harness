package test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/ecosystem"
	"kh/internal/session"
)

// Verify instruction delivery, not model compliance or a deterministic tool ban.
func TestEcosystemManagerPolicyRootResumeAndPromotion(t *testing.T) {
	binary, requests := newEcosystemProviderRequests(t, func(step int) *projectCLICall {
		if step == 2 { // Fresh root and resumed root finish; a worker promotes.
			return &projectCLICall{Name: "tool_invoke", Arguments: `{"name":"agents","arguments":{"action":"set_role","role":"manager"}}`}
		}
		return nil
	})
	projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), ".kh", "config.json"), `{"system":"CUSTOM-SYSTEM","auto":true,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = strings.NewReader("")
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "PROJECT-CLI-DONE") {
			t.Fatalf("fixture %v: %s: %v", args, out, err)
		}
	}
	checkPolicy := func(label string) projectCLIRequest {
		t.Helper()
		select {
		case request := <-requests:
			for _, want := range []string{
				"CUSTOM-SYSTEM", "Role duties follow your current role, including after set_role",
				"Every manager (the root and promoted descendants)",
				"implementation and integration edits, routine test execution, builds, installation and restart execution to workers",
				"having no active workers does not transfer execution to the manager",
				"On promotion to manager, hand any existing coding or execution task to workers before continuing",
				"Do not switch roles to bypass delegation", "do not become a worker while managing live descendants",
				"Managers may answer ordinary informational questions directly", "bounded read-only inspection",
				"Workers execute their assigned scope and report evidence; they need not promote or delegate",
				"At capacity, retain orchestration", "never fall back to doing the execution yourself",
			} {
				if !strings.Contains(request.Instructions, want) {
					t.Errorf("%s instructions missing %q", label, want)
				}
			}
			assertCompactToolSchemas(t, request)
			return request
		default:
			t.Fatalf("%s: no provider request", label)
			return projectCLIRequest{}
		}
	}
	run(nil, "-p", "complete root task")
	checkPolicy("fresh root")
	// Loading the actual saved root history must preserve current runtime policy.
	savedID, err := session.Latest()
	if err != nil {
		t.Fatal(err)
	}
	run(nil, "-s", savedID, "-p", "complete resumed task")
	resumed := checkPolicy("resumed root")
	history, _ := json.Marshal(resumed.Input)
	if !strings.Contains(string(history), "complete root task") {
		t.Fatalf("resumed provider history missing original root task: %s", history)
	}

	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	process, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateRoot(ctx, ecosystem.RootOptions{Name: "policy-manager", MaxAgents: 3, Process: process})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := store.Reserve(ctx, root.ID, "policy-worker", "assigned coding task")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, process); err != nil {
		t.Fatal(err)
	}
	run([]string{"KH_ECOSYSTEM_ID=" + root.EcosystemID, "KH_ECOSYSTEM_AGENT_ID=" + reservation.Agent.ID, "KH_ECOSYSTEM_TOKEN=" + reservation.Token, "KH_PARENT=" + root.Address}, "-p", "complete worker task")
	spawned := checkPolicy("spawned worker")
	if !strings.Contains(spawned.Instructions, "parent: "+root.Address) || !strings.Contains(spawned.Instructions, "Discover the agents operation") {
		t.Error("compact provider lost child coordination identity")
	}
	promoted := checkPolicy("promoted manager")
	encoded, _ := json.Marshal(promoted.Input)
	for _, want := range []string{`\"role\":\"manager\"`, "Manager role: hand any existing coding or execution task to workers before continuing", "routine tests, builds, installation and restart execution", "no workers are active or capacity is full"} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("promotion result missing %q: %s", want, encoded)
		}
	}
	worker, err := store.Get(ctx, reservation.Agent.ID)
	if err != nil || worker.Role != ecosystem.Manager {
		t.Fatalf("promotion not applied: %+v: %v", worker, err)
	}
	// The same operation through the CLI remains a pure JSON snapshot, without
	// the native tool's instruction reminder or tool-result snapshot suffix.
	cmd := exec.CommandContext(ctx, binary, "set_role", "manager")
	cmd.Stdin = strings.NewReader("")
	cmd.Env = append(os.Environ(), "KH_ECOSYSTEM_ID="+root.EcosystemID, "KH_ECOSYSTEM_AGENT_ID="+root.ID)
	out, err := cmd.CombinedOutput()
	var snapshot struct {
		Self ecosystem.Agent `json:"self"`
	}
	if err != nil || json.Unmarshal(out, &snapshot) != nil || snapshot.Self.Role != ecosystem.Manager {
		t.Fatalf("CLI set_role JSON contract changed: %s: %v", out, err)
	}
}
