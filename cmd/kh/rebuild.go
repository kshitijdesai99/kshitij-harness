package main

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Rebuild the executable that was actually invoked, not an unrelated kh on
// PATH. Build beside it and rename only after success: failed builds leave the
// old executable usable, and existing chats keep running their old image.
func rebuild(ctx context.Context) error {
	return rebuildBinary(ctx, false)
}

// The marker is valid only for this PID across exec, and is consumed before
// launching any children. New sessions and agents must do their own rebuild.
func rebuildBeforeSession(ctx context.Context) (bool, error) {
	ready := os.Getenv("KH_REBUILD_EXEC_PID")
	os.Unsetenv("KH_REBUILD_EXEC_PID")
	if ready == strconv.Itoa(os.Getpid()) {
		return true, nil
	}
	if os.Getenv("KH_AUTO_REBUILD") == "0" {
		return false, nil
	}
	if err := rebuildBinary(ctx, true); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	env := append(os.Environ(), "KH_REBUILD_EXEC_PID="+strconv.Itoa(os.Getpid()))
	return false, syscall.Exec(exe, os.Args, env)
}

func rebuildBinary(ctx context.Context, starting bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	repo, err := rebuildSource(exe, starting)
	if err != nil {
		return err
	}
	original, err := os.Stat(exe)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(exe), ".kh-rebuild-*")
	if err != nil {
		return fmt.Errorf("cannot create rebuild output beside %s: %w", exe, err)
	}
	output := temp.Name()
	defer os.Remove(output)
	if err := temp.Close(); err != nil {
		return err
	}
	out := os.Stdout
	if starting {
		out = os.Stderr
	}
	fmt.Fprintf(out, "Rebuilding %s from %s...\n", exe, repo)
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=exe", "-o", output, "./cmd/kh")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	cmd.Stdout, cmd.Stderr = out, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fmt.Errorf("rebuild failed; existing binary unchanged: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := buildinfo.ReadFile(output)
	if err != nil {
		return fmt.Errorf("invalid rebuild output; existing binary unchanged: %w", err)
	}
	if info.Path != "kh/cmd/kh" || info.Main.Path != "kh" {
		return fmt.Errorf("unexpected rebuild output %q; existing binary unchanged", info.Path)
	}
	if err := os.Chmod(output, original.Mode().Perm()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(output, exe); err != nil {
		return fmt.Errorf("install rebuilt binary: %w", err)
	}
	if starting {
		fmt.Fprintf(out, "Rebuilt %s. Starting session with the new build.\n", exe)
	} else {
		fmt.Fprintf(os.Stdout, "Rebuilt %s. Existing chats are unchanged; restart this executable with -r to load this build.\n", exe)
	}
	return nil
}

// Automatic builds belong to the invoked binary's checkout, not whichever
// checkout happens to be the working folder. Explicit --rebuild retains the
// current-checkout preference. A moved/trimpath install can use KH_SOURCE_DIR.
func rebuildSource(exe string, automatic bool) (string, error) {
	if dir := os.Getenv("KH_SOURCE_DIR"); dir != "" {
		dir, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		if !isHarnessSource(dir) {
			return "", fmt.Errorf("KH_SOURCE_DIR is not a kh source checkout: %s", dir)
		}
		return dir, nil
	}
	embedded := ""
	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		embedded = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	}
	if automatic && isHarnessSource(embedded) {
		return embedded, nil
	}
	starts := []string{filepath.Dir(exe)}
	if !automatic {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		starts = append([]string{cwd}, starts...)
	}
	for _, start := range starts {
		for dir := start; ; dir = filepath.Dir(dir) {
			if isHarnessSource(dir) {
				return dir, nil
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	if isHarnessSource(embedded) {
		return embedded, nil
	}
	return "", fmt.Errorf("cannot locate kh sources; set KH_SOURCE_DIR to its checkout (or KH_AUTO_REBUILD=0 to skip automatic builds)")
}

func isHarnessSource(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	module := ""
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			module = fields[1]
			break
		}
	}
	if module != "kh" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "cmd", "kh", "main.go"))
	return err == nil && info.Mode().IsRegular()
}
