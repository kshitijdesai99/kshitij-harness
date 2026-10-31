package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"kh/internal/ecosystem"
)

// These are real idle CLI instances, but private HOME/cwd/socket and no task,
// credentials, or provider requests. Only fixture reservations change the tree.
func TestLiveDescendantsIdleTmux(t *testing.T) {
	f := newEcosystemCLI(t)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".kh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".kh", "config.json"), []byte(`{"memory_model":"off","memory_hybrid":false,"map_cap":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	rootPane := f.runTmux("display-message", "-p", "-t", "eco:root", "#{pane_id}")
	const custom = "  literal=##W/##{window_name} name=#{window_name}  "
	f.runTmux("set-option", "-w", "-t", rootPane, "window-status-format", custom)
	const comparison = "#{?#{==:#{window_name},My custom tab},YES,NO} #W"
	f.runTmux("set-option", "-w", "-t", rootPane, "window-status-current-format", comparison)
	f.runTmux("rename-window", "-t", rootPane, "My custom tab")
	f.runTmux("respawn-pane", "-k", "-t", rootPane, "env KH_PROVIDER=codex KH_AGENT=kh:root "+quote(f.binary))
	f.waitOutput(rootPane, "kh chat, session")
	wait := func(label string, probe func() (string, bool)) string {
		t.Helper()
		deadline := time.Now().Add(7 * time.Second)
		var got string
		for {
			var ok bool
			got, ok = probe()
			if ok {
				return got
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: last value %q", label, got)
			}
			time.Sleep(40 * time.Millisecond)
		}
	}
	value := func(pane, format string) string { return f.runTmux("display-message", "-p", "-t", pane, format) }
	count := func(pane string, n int, role string) {
		t.Helper()
		want := strconv.Itoa(n) + " - " + role
		wait("idle pane count "+pane+"="+want, func() (string, bool) { got := value(pane, "#{@kh_live_descendants}"); return got, got == want })
		wait("idle title count "+pane+"="+want, func() (string, bool) {
			got := value(pane, "#{pane_title}")
			return got, strings.HasSuffix(got, " ("+want+")")
		})
	}
	rootID := value(rootPane, "#{@kh_agent_id}")
	root, err := store.Get(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	count(rootPane, 0, "M")
	// The format must retain whitespace and literal escaped tokens exactly.
	statusFormat := func(pane, option string) string {
		t.Helper()
		cmd := exec.Command("tmux", "-L", f.socket, "show-options", "-A", "-w", "-v", "-t", pane, option)
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("read status format: %s: %v", out, err)
		}
		return strings.TrimSuffix(string(out), "\n")
	}
	format := statusFormat(rootPane, "window-status-format")
	if !strings.Contains(format, "literal=##W/##{window_name} name=#{window_name}") || strings.Count(format, "@kh_live_descendants") != 2 || !strings.HasPrefix(format, "  literal=") || !strings.HasSuffix(format, "  ") {
		t.Fatalf("custom format/literal tokens changed or suffix duplicated: %q", format)
	}
	// display-message expands a supplied status format just as the status line does.
	rendered := value(rootPane, format)
	if rendered != "literal=#W/#{window_name} name=My custom tab (0 - M)" {
		t.Fatalf("rendered custom format: %q", rendered)
	}
	originalFormat := format
	currentFormat := statusFormat(rootPane, "window-status-current-format")
	if got := value(rootPane, currentFormat); got != "YES My custom tab (0 - M)" {
		t.Fatalf("nested format comparison changed semantics: %q (%q)", got, currentFormat)
	}
	reservation, err := store.Reserve(ctx, root.ID, "manager", "idle manager fixture")
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, launcher); err != nil {
		t.Fatal(err)
	}
	managerCommand := fmt.Sprintf("env KH_PROVIDER=codex KH_AGENT=kh:manager KH_PARENT=%s KH_ECOSYSTEM_ID=%s KH_ECOSYSTEM_AGENT_ID=%s KH_ECOSYSTEM_TOKEN=%s %s", quote(root.Address), quote(root.EcosystemID), quote(reservation.Agent.ID), quote(reservation.Token), quote(f.binary))
	managerPane := f.runTmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "eco", "-n", "Manager custom tab", managerCommand)
	f.waitOutput(managerPane, "kh chat, session")
	manager, err := store.Get(ctx, reservation.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Leaf zeroes and unchanged-count role switches refresh without model turns.
	count(managerPane, 0, "W")
	for _, role := range []ecosystem.Role{ecosystem.Manager, ecosystem.Worker, ecosystem.Manager} {
		if err := store.SetRole(ctx, manager.ID, role); err != nil {
			t.Fatal(err)
		}
		label := "M"
		if role == ecosystem.Worker {
			label = "W"
		}
		count(managerPane, 0, label)
		got := value(managerPane, statusFormat(managerPane, "window-status-current-format"))
		if !strings.Contains(got, "Manager custom tab (0 - "+label+")") {
			t.Fatalf("idle role switch not reflected in tab: %q", got)
		}
	}
	count(rootPane, 1, "M")
	// This window inherits tmux's defaults rather than an explicit local format.
	inherited := f.runTmux("show-options", "-A", "-w", "-v", "-t", managerPane, "window-status-current-format")
	if !strings.Contains(inherited, "@kh_live_descendants") {
		t.Fatalf("inherited status format not decorated: %q", inherited)
	}
	nested, err := store.Reserve(ctx, manager.ID, "nested", "nested fixture")
	if err != nil {
		t.Fatal(err)
	}
	count(rootPane, 2, "M")
	count(managerPane, 1, "M")
	if got := value(rootPane, currentFormat); got != "YES My custom tab (2 - M)" {
		t.Fatalf("nested comparison/count update: %q", got)
	}
	if got := value(rootPane, "#{window_name}"); got != "My custom tab" {
		t.Fatalf("custom tab renamed: %q", got)
	}
	if got := value(managerPane, "#{window_name}"); got != "Manager custom tab" {
		t.Fatalf("manager tab renamed: %q", got)
	}
	if err := store.SetRole(ctx, manager.ID, ecosystem.Worker); err != nil {
		t.Fatal(err)
	}
	count(managerPane, 1, "W") // Role changes do not alter the descendant count.
	if err := store.CancelReservation(ctx, nested.Agent.ID, nested.Token); err != nil {
		t.Fatal(err)
	}
	count(rootPane, 1, "M")
	count(managerPane, 0, "W")
	if got := statusFormat(rootPane, "window-status-format"); got != originalFormat {
		t.Fatalf("idle refresh stacked/changed custom format: %q -> %q", originalFormat, got)
	}
	// Keep the source window so returning later exercises idempotent decoration.
	sourcePeer := f.runTmux("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", rootPane, f.helper)
	f.waitOutput(sourcePeer, "CONSOLE_READY")
	// Moving the same live pane refreshes its destination window's format.
	destination := f.runTmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "eco", "-n", "Moved custom tab", f.helper)
	f.waitOutput(destination, "CONSOLE_READY")
	const shellOperand = " #(printf 'literal-#W-#{window_name}')"
	f.runTmux("set-option", "-w", "-t", destination, "window-status-current-format", "moved:#W:end"+shellOperand)
	f.runTmux("set-option", "-w", "-t", destination, "window-status-format", "  no name token  ")
	f.runTmux("join-pane", "-d", "-s", rootPane, "-t", destination)
	f.runTmux("select-pane", "-t", rootPane)
	wait("moved window decoration", func() (string, bool) {
		got := f.runTmux("show-options", "-w", "-v", "-t", rootPane, "window-status-current-format")
		return got, strings.Contains(got, "@kh_live_descendants")
	})
	count(rootPane, 1, "M")
	if got := statusFormat(rootPane, "window-status-current-format"); !strings.HasSuffix(got, shellOperand) || strings.Count(got, "@kh_live_descendants") != 2 {
		t.Fatalf("shell operand changed or top-level suffix duplicated: %q", got)
	}
	if got := value(rootPane, "#{window_name}"); got != "Moved custom tab" {
		t.Fatalf("move renamed destination: %q", got)
	}
	if got := statusFormat(rootPane, "window-status-format"); got != "  no name token  " {
		t.Fatalf("custom no-name format changed: %q", got)
	}
	f.runTmux("join-pane", "-d", "-s", rootPane, "-t", sourcePeer)
	f.runTmux("select-pane", "-t", rootPane)
	time.Sleep(1200 * time.Millisecond) // Allow the poller to revisit the original window.
	if got := statusFormat(rootPane, "window-status-format"); got != originalFormat {
		t.Fatalf("window re-entry duplicated suffix: %q", got)
	}
	// Retain the exiting pane so cleanup can be observed, not hidden by tmux.
	f.runTmux("set-option", "-w", "-t", managerPane, "remain-on-exit", "on")
	f.runTmux("send-keys", "-t", managerPane, "C-d")
	wait("manager exit", func() (string, bool) { got := value(managerPane, "#{pane_dead}"); return got, got == "1" })
	if got := value(managerPane, "#{@kh_live_descendants}"); got != "" {
		t.Fatalf("graceful exit retained pane counter: %q", got)
	}
	count(rootPane, 0, "M") // Root reconciles the exact exited manager while idle.

	// A failed one-shot task with no credentials exits locally before any
	// provider request. Its retained pane must also clear an owned old suffix.
	failedPane := f.runTmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "eco", "-n", "Failed custom tab", f.helper)
	f.waitOutput(failedPane, "CONSOLE_READY")
	f.runTmux("set-option", "-w", "-t", failedPane, "remain-on-exit", "on")
	f.runTmux("set-option", "-p", "-t", failedPane, "@kh_live_descendants", "99")
	f.runTmux("respawn-pane", "-k", "-t", failedPane, "env KH_PROVIDER=codex KH_AGENT=kh:failed KH_PARENT= KH_ECOSYSTEM_ID= KH_ECOSYSTEM_AGENT_ID= KH_ECOSYSTEM_TOKEN= "+quote(f.binary)+" -p 'must fail without credentials'")
	f.waitOutput(failedPane, "not logged in")
	wait("failed CLI exit", func() (string, bool) { got := value(failedPane, "#{pane_dead}"); return got, got == "1" })
	if got := value(failedPane, "#{@kh_agent_id}"); got == "" {
		t.Fatal("failed fixture never acquired pane ownership")
	}
	if got := value(failedPane, "#{@kh_live_descendants}"); got != "" {
		t.Fatalf("error exit retained stale pane counter: %q", got)
	}
}
