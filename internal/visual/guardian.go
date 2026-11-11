// Package visual owns session-root marimo companions and their artifacts.
package visual

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"kh/internal/ecosystem"
)

// GuardianArgument is an internal entry point, not a model-callable command.
const GuardianArgument = "__kh_visual_guardian"

type launch struct {
	Argv  []string
	Dir   string
	Env   []string
	Owner ecosystem.ProcessIdentity
	Stop  string
}

// GuardianMain runs in a detached session with a private launch pipe.
// The guardian never signals a saved PID: it kills its own still-live group.
// Servers must not daemonize or escape that group.
func GuardianMain() error {
	if syscall.Getpgrp() != os.Getpid() {
		return fmt.Errorf("visual guardian requires its own process group")
	}
	owner := os.NewFile(3, "visual-owner")
	if owner == nil {
		return fmt.Errorf("missing visual guardian pipe")
	}
	info, err := owner.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return fmt.Errorf("invalid visual owner pipe")
	}
	var spec launch
	decoder := json.NewDecoder(owner)
	if err := decoder.Decode(&spec); err != nil {
		return err
	}
	if len(spec.Argv) == 0 {
		return fmt.Errorf("missing visual command")
	}
	// Catch direct termination as well as owner EOF, then terminate the group.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	eof := make(chan struct{})
	go func() {
		if !spec.Owner.Valid() {
			io.Copy(io.Discard, owner)
		} else {
			// Transfer only after startup succeeds and a discoverable manifest
			// exists. A worker dying during startup must not leak a server.
			var committed bool
			if err := decoder.Decode(&committed); err != nil || !committed {
				close(eof)
				return
			}
			// The launcher's pipe is no longer ownership after commit.
			owner.Close()
			return
		}
		close(eof)
	}()
	var sessionDone chan struct{}
	if spec.Owner.Valid() {
		sessionDone = make(chan struct{})
		go func() {
			defer close(sessionDone)
			for {
				alive, err := ecosystem.ProbeProcess(spec.Owner)
				_, stopErr := os.Stat(spec.Stop)
				_, closeErr := os.Stat(filepath.Join(spec.Dir, "state", "closed"))
				if (err == nil && !alive) || stopErr == nil || closeErr == nil {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-eof:
	case <-sessionDone:
	case <-done:
	case <-signals:
	}
	// Allow marimo's normal teardown (including notebook kernels) before
	// forcing remaining members of our own group down. The guardian catches its
	// own TERM and remains the group leader throughout; no saved PID is killed.
	_ = syscall.Kill(0, syscall.SIGTERM)
	select {
	case <-done:
		// Forwarded shutdown may still be finishing in a private child group.
		time.Sleep(100 * time.Millisecond)
	case <-time.After(3 * time.Second):
	}
	// Self remains group leader until this atomic signal. PID/group reuse cannot
	// redirect cleanup at unrelated sessions, even after the server has exited.
	if spec.Stop != "" {
		// Orphaned guardians may briefly be zombies on systems whose PID 1
		// reaps slowly. Publish completion without mistaking that for liveness.
		_ = os.WriteFile(spec.Stop+".done", nil, 0600)
	}
	return syscall.Kill(0, syscall.SIGKILL)
}
