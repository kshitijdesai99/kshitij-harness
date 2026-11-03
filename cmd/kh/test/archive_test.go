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

func TestConversationArchiveNewResumeToolsAndLiteralInput(t *testing.T) {
	binary, requests := newEcosystemProviderRequests(t, func(step int) *projectCLICall {
		if step == 0 {
			return &projectCLICall{Name: "bash", Arguments: `{"command":"printf archive-tool"}`}
		}
		return nil
	})
	projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), ".agents", "commands", "archive-test.md"), "Expanded command instructions.")
	run := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = strings.NewReader("")
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	run("-p", "/archive-test")
	id, err := session.Latest()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(session.Folder(), id+".jsonl")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	request := <-requests
	requestJSON, _ := json.Marshal(request.Input)
	if !strings.Contains(string(requestJSON), "Expanded command instructions.") {
		t.Fatal("fixture did not expand command")
	}
	run("-s", id, "-p", "continue")
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), string(first)) {
		t.Fatal("resume rewrote archive")
	}
	var origins, users []string
	calls, results, assistant := 0, 0, 0
	err = session.ReadArchive(strings.NewReader(string(data)), func(r session.ArchiveRecord) error {
		switch r.Type {
		case "session_start":
			origins = append(origins, r.Origin)
		case "user_message":
			users = append(users, r.Text)
		case "tool_call":
			calls++
		case "tool_result":
			results++
		case "assistant_delta":
			assistant++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(origins, ",") != "new,resume" || strings.Join(users, ",") != "/archive-test,continue" || assistant < 2 || calls != 1 || results != 1 {
		t.Fatalf("origins=%v users=%v calls=%d results=%d assistant=%d", origins, users, calls, results, assistant)
	}
}

func TestConversationArchiveLegacyResumeStartsNow(t *testing.T) {
	binary, _ := newEcosystemProviderRequests(t, func(int) *projectCLICall { return nil })
	legacy := []byte(`{"session":"legacy","map":"","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"PRIVATE_OLD_HISTORY"}]}]}`)
	if err := session.Save("old", legacy); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-s", "old", "-p", "new input")
	cmd.Stdin = strings.NewReader("")
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	archive, err := os.ReadFile(filepath.Join(session.Folder(), "old.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(archive), "PRIVATE_OLD_HISTORY") || !strings.Contains(string(archive), `"origin":"resume"`) {
		t.Fatalf("backfill: %s", archive)
	}
}
