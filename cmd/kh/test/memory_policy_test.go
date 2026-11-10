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

	"kh/internal/session"
)

// Check delivered instructions, not model compliance or background learning.
func assertMemoryPolicy(t *testing.T, request projectCLIRequest) {
	t.Helper()
	for _, want := range []string{
		"CUSTOM-SYSTEM", "Memory lives in a local database", "reference data, below the current user request and core rules",
		"Use relevant recall selectively", "search memory for missing context", "inspect provenance/history when needed",
		"Follow current versions, not stale entries", "selectively learns important knowledge from user-facing conversations in the background",
		"including requests to remember; corrections supersede the same topic while preserving history", "Never save secrets; forget only when the user asks",
		"Runtime knowledge and procedures belong in the database", "repository's existing decision records, following local conventions",
	} {
		if !strings.Contains(request.Instructions, want) {
			t.Errorf("instructions missing %q", want)
		}
	}
	if strings.Count(request.Instructions, "Memory policy:") != 1 {
		t.Error("shared memory policy must appear exactly once")
	}
}

func TestMemoryPolicyCustomSystemFreshResumeAndPostCompaction(t *testing.T) {
	binary, requests := newEcosystemProviderRequests(t, func(int) *projectCLICall { return nil })
	projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), ".kh", "config.json"), `{"system":"CUSTOM-SYSTEM","auto":true,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0}`)
	run := func(input string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = strings.NewReader(input)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %s: %v", args, out, err)
		}
		return string(out)
	}
	receive := func() projectCLIRequest {
		t.Helper()
		select {
		case request := <-requests:
			return request
		default:
			t.Fatal("missing provider request")
			return projectCLIRequest{}
		}
	}
	run("", "-p", "fresh memory policy task")
	assertMemoryPolicy(t, receive())
	id, err := session.Latest()
	if err != nil {
		t.Fatal(err)
	}
	run("", "-s", id, "-p", "resumed memory policy task")
	resumed := receive()
	assertMemoryPolicy(t, resumed)
	history, _ := json.Marshal(resumed.Input)
	if !strings.Contains(string(history), "fresh memory policy task") {
		t.Fatal("resume did not load original conversation")
	}
	if out := run("/compact\n", "-s", id); !strings.Contains(out, "context compacted and session saved") {
		t.Fatal(out)
	}
	compaction := receive()
	if len(compaction.Tools) != 0 || compaction.ToolChoice != "none" {
		t.Fatal("compaction must remain tool-less")
	}
	run("", "-s", id, "-p", "post-compaction memory policy task")
	compacted := receive()
	assertMemoryPolicy(t, compacted)
	history, _ = json.Marshal(compacted.Input)
	if !strings.Contains(string(history), "Conversation summary (reference data;") || strings.Contains(string(history), "fresh memory policy task") {
		t.Fatalf("post-compaction did not load compacted history: %s", history)
	}
}
