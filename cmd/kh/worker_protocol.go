package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"io"
	"kh/internal/config"
	"kh/internal/delegation"
	"kh/internal/ecosystem"
	"kh/internal/terminal"
)

type workerSpawn struct {
	name, task  string
	keep, reuse bool
	handoff     *delegation.Handoff
}
type workerAssignment struct {
	Lease   ecosystem.TaskLease `json:"lease"`
	Handoff delegation.Handoff  `json:"handoff"`
}

func canonicalDirectory() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(cwd)
}
func parseWorkerSpawn(args []string) (workerSpawn, error) {
	var s workerSpawn
	args = args[1:]
	structured := false
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--keep":
			s.keep = true
		case "--reuse":
			s.reuse = true
		case "--handoff":
			structured = true
		default:
			return s, fmt.Errorf("unknown spawn option %s", args[0])
		}
		args = args[1:]
	}
	if len(args) != 2 || !agentName.MatchString(args[0]) || args[0] == "main" || strings.TrimSpace(args[1]) == "" || (s.keep && s.reuse) {
		return s, fmt.Errorf("usage: kh spawn [--keep|--reuse] [--handoff] <name> <task or handoff JSON>")
	}
	s.name, s.task = args[0], args[1]
	if structured && s.keep {
		return s, fmt.Errorf("keep with structured handoff is unsupported; use reuse for task-fresh follow-ups")
	}
	if structured {
		var h delegation.Handoff
		if err := json.Unmarshal([]byte(s.task), &h); err != nil {
			return s, err
		}
		if err := h.Validate(); err != nil {
			return s, err
		}
		s.handoff = &h
		s.task = h.Task
	}
	if s.reuse && s.handoff == nil {
		cwd, err := canonicalDirectory()
		if err != nil {
			return s, err
		}
		s.handoff = &delegation.Handoff{Version: 1, Task: s.task, Directory: cwd}
	}
	if s.handoff != nil {
		if err := s.handoff.Validate(); err != nil {
			return s, err
		}
		cwd, err := canonicalDirectory()
		if err != nil {
			return s, err
		}
		if s.handoff.Directory != cwd {
			return s, fmt.Errorf("worker handoff directory must match the launch directory")
		}
	}
	return s, nil
}
func workerCompatibility(directory string, cfg config.Config) string {
	b, _ := json.Marshal([]any{directory, cfg.Provider, cfg.Model, cfg.Effort, cfg.Auto})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (e *ecosystemRuntime) reuseTask(ctx context.Context, s workerSpawn, cfg config.Config) (string, bool, error) {
	agents, err := e.store.List(ctx, e.self.EcosystemID)
	if err != nil {
		return "", false, err
	}
	for _, a := range agents {
		if a.ParentID != e.self.ID || a.Role != ecosystem.Worker || a.State != ecosystem.Idle || a.Pane.PaneID == "" {
			continue
		}
		status, err := e.store.TaskStatus(ctx, a.ID)
		if err != nil {
			return "", false, err
		}
		if !status.Reusable {
			continue
		}
		compatibility, err := tmuxContext(ctx, "display-message", "-p", "-t", a.Pane.PaneID, "#{@kh_worker_config}")
		if err != nil || compatibility != workerCompatibility(s.handoff.Directory, cfg) {
			continue
		}
		out, err := e.assignTask(ctx, []string{"assign", a.Address, mustJSON(s.handoff)}, cfg)
		if err != nil {
			// Once a compatible worker was selected, assignment may already have been
			// accepted despite a transport error. Never duplicate work by cold spawning.
			return "", true, err
		}
		return out, true, nil
	}
	return "", false, nil
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// Only a random receipt is transported through the terminal. The private,
// bounded payload file is deleted on receipt and never contains parent history.
func stageWorkerAssignment(agentID string, a workerAssignment) (json.RawMessage, error) {
	dir := filepath.Join(config.Dir(), "handoffs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("handoff directory must be private")
	}
	f, err := os.CreateTemp(dir, "task-"+agentID+"-*.json")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	b, err := json.Marshal(a)
	if err == nil {
		_, err = f.Write(b)
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	return json.Marshal(struct {
		File string `json:"file"`
	}{filepath.Base(name)})
}
func receiveWorkerAssignment(agentID string, raw []byte) (workerAssignment, error) {
	var a workerAssignment
	var receipt struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return a, err
	}
	if receipt.File == "" || filepath.Base(receipt.File) != receipt.File || !strings.HasPrefix(receipt.File, "task-"+agentID+"-") || !strings.HasSuffix(receipt.File, ".json") {
		return a, fmt.Errorf("invalid task receipt")
	}
	path := filepath.Join(config.Dir(), "handoffs", receipt.File)
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return a, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	defer os.Remove(path)
	info, err := f.Stat()
	if err != nil {
		return a, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm() != 0600 || !info.Mode().IsRegular() || info.Size() > delegation.MaxHandoffBytes*8 {
		return a, fmt.Errorf("invalid task payload file")
	}
	b, err := io.ReadAll(io.LimitReader(f, delegation.MaxHandoffBytes*8+1))
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(b, &a)
	return a, err
}
func cleanupWorkerAssignments(agentID string) {
	dir := filepath.Join(config.Dir(), "handoffs")
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "task-"+agentID+"-") && strings.HasSuffix(entry.Name(), ".json") {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func (e *ecosystemRuntime) assignTask(ctx context.Context, args []string, cfg config.Config) (string, error) {
	if len(args) != 3 || !validAddress(args[1]) {
		return "", fmt.Errorf("usage: kh assign kh:<address> <handoff JSON>")
	}
	var h delegation.Handoff
	if err := json.Unmarshal([]byte(args[2]), &h); err != nil {
		return "", err
	}
	if err := h.Validate(); err != nil {
		return "", err
	}
	a, err := e.store.Resolve(ctx, e.self.EcosystemID, args[1])
	if err != nil {
		return "", err
	}
	compatibility, err := tmuxContext(ctx, "display-message", "-p", "-t", a.Pane.PaneID, "#{@kh_worker_config}")
	if err != nil {
		return "", err
	}
	if compatibility != workerCompatibility(h.Directory, cfg) {
		return "", fmt.Errorf("worker configuration/directory differs; spawn a compatible worker")
	}
	lease, err := e.store.ReserveTask(ctx, e.self.ID, a.ID, h.Task)
	if err != nil {
		return "", err
	}
	payload, err := stageWorkerAssignment(a.ID, workerAssignment{lease, h})
	if err != nil {
		rollback := e.store.CancelTaskReservation(context.WithoutCancel(ctx), e.self.ID, a.ID, a.Process, lease)
		return "", errors.Join(err, rollback)
	}
	err = e.sendWorkerControl(ctx, a, terminal.TaskControl{Kind: "assign", Generation: lease.Generation, Payload: payload})
	if err != nil {
		rollback := e.store.CancelTaskReservation(context.WithoutCancel(ctx), e.self.ID, a.ID, a.Process, lease)
		var receipt struct {
			File string `json:"file"`
		}
		if json.Unmarshal(payload, &receipt) == nil && filepath.Base(receipt.File) == receipt.File {
			_ = os.Remove(filepath.Join(config.Dir(), "handoffs", receipt.File))
		}
		if rollback == nil {
			return "", fmt.Errorf("task delivery failed; pending assignment cancelled: %w", err)
		}
		return "", fmt.Errorf("task %s delivery unconfirmed and may already be running; inspect worker before retrying: %w", lease.ID, errors.Join(err, rollback))
	}
	return fmt.Sprintf("assigned fresh task %s to %s (generation %d)", lease.ID, a.Address, lease.Generation), nil
}
func (e *ecosystemRuntime) sendWorkerControl(ctx context.Context, a ecosystem.Agent, control terminal.TaskControl) error {
	marker, err := tmuxContext(ctx, "display-message", "-p", "-t", a.Pane.PaneID, "#{@kh_task_input}")
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(marker)
	if err != nil {
		return fmt.Errorf("worker is not ready for task input")
	}
	identity, err := ecosystem.ProcessForPID(pid)
	if err != nil || identity != a.Process {
		return fmt.Errorf("worker receiver identity changed")
	}
	frame, err := terminal.TaskFrame(control)
	if err != nil {
		return err
	}
	// Each control frame is one atomic tmux command. Assignments carry only a
	// short private-file receipt, so large handoffs cannot interleave with peers
	// or human keystrokes and never hit tmux's per-command size limit.
	if len(frame) > 2048 {
		return fmt.Errorf("peer message too large for isolated task input")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	keys := []string{"send-keys", "-H", "-t", a.Pane.PaneID}
	for _, b := range []byte(frame) {
		keys = append(keys, fmt.Sprintf("%02x", b))
	}
	_, err = tmuxContext(context.WithoutCancel(ctx), keys...)
	return err
}
