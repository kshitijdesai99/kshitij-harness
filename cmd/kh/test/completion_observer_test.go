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

func TestCompletionObserverIdleBusyAndResume(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	binary, requests := newEcosystemProviderRequests(t, func(step int) *projectCLICall {
		if step == 0 {
			close(entered)
			<-release
		}
		return nil
	})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	log, err := os.Create(filepath.Join(t.TempDir(), "output"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	app := exec.CommandContext(ctx, binary)
	input, err := app.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	app.Stdout, app.Stderr = log, log
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	defer app.Process.Kill()
	read := func() string { b, _ := os.ReadFile(log.Name()); return string(b) }
	wait := func(text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(read(), text) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("missing %q: %s", text, read())
	}
	// A local slash command synchronizes with fully initialized console startup.
	if _, err := input.Write([]byte("/auto\n")); err != nil {
		t.Fatal(err)
	}
	wait("auto on:")
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := onlyEcosystemRoot(t, store)
	process, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := store.Reserve(ctx, root.ID, "completion-fixture", "PRIVATE-TASK-SENTINEL")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, process); err != nil {
		t.Fatal(err)
	}
	child, err := store.Claim(ctx, reservation.Agent.ID, reservation.Token, process, ecosystem.PaneInfo{})
	if err != nil {
		t.Fatal(err)
	}
	publish := func(id, text string) {
		t.Helper()
		if err := store.PublishResult(ctx, child.ID, id, text); err != nil {
			t.Fatal(err)
		}
	}
	publish("idle", "IDLE-COMPLETION-SENTINEL")
	wait("IDLE-COMPLETION-SENTINEL")
	select {
	case r := <-requests:
		t.Fatalf("idle completion triggered request: %+v", r)
	default:
	}
	if _, err := input.Write([]byte("PRIVATE-USER-SENTINEL\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("provider not entered")
	}
	publish("busy", "BUSY-COMPLETION-SENTINEL")
	wait("BUSY-COMPLETION-SENTINEL")
	close(release)
	released = true
	wait("PROJECT-CLI-DONE")
	// Wait across multiple polls to detect repeated rendering or injected turns.
	time.Sleep(600 * time.Millisecond)
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := app.Wait(); err != nil {
		t.Fatalf("shutdown: %v: %s", err, read())
	}
	for _, text := range []string{"IDLE-COMPLETION-SENTINEL", "BUSY-COMPLETION-SENTINEL"} {
		if n := strings.Count(read(), text); n != 1 {
			t.Fatalf("completion %q rendered %d times: %s", text, n, read())
		}
	}
	if strings.Contains(read(), "PRIVATE-TASK-SENTINEL") {
		t.Fatal("private task leaked to completion output")
	}
	select {
	case request := <-requests:
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "COMPLETION-SENTINEL") {
			t.Fatalf("completion injected into provider history: %s", data)
		}
	case <-ctx.Done():
		t.Fatal("missing user request")
	}
	select {
	case r := <-requests:
		t.Fatalf("completion injected extra request: %+v", r)
	default:
	}
	id, err := session.Latest()
	if err != nil {
		t.Fatal(err)
	}
	// Keeping stdin open for several polls verifies historical suppression, not
	// merely an EOF exit before the observer's first tick.
	resumed := exec.CommandContext(ctx, binary, "-s", id)
	resumedInput, err := resumed.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	resumedLog, err := os.Create(filepath.Join(t.TempDir(), "resumed"))
	if err != nil {
		t.Fatal(err)
	}
	defer resumedLog.Close()
	resumed.Stdout, resumed.Stderr = resumedLog, resumedLog
	if err := resumed.Start(); err != nil {
		t.Fatal(err)
	}
	defer resumed.Process.Kill()
	time.Sleep(800 * time.Millisecond)
	resumedInput.Close()
	if err := resumed.Wait(); err != nil {
		t.Fatalf("resume shutdown: %v", err)
	}
	data, _ := os.ReadFile(resumedLog.Name())
	if strings.Contains(string(data), "COMPLETION-SENTINEL") {
		t.Fatalf("historical completion replayed: %s", data)
	}
	select {
	case r := <-requests:
		t.Fatalf("resume triggered request: %+v", r)
	default:
	}
}
