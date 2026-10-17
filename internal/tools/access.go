package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"kh/internal/config"
)

// WriteApprover asks for session-scoped access independently of command approval.
type WriteApprover interface {
	ApproveWrite(context.Context, string) (bool, error)
}

type writeGrant struct {
	dir  string
	root *os.Root
}

type writeRequest struct {
	done chan struct{}
	err  error
}

// WriteAccess owns approved directory handles until Close. Configured Writable
// directories are bash permissions only, not implicit permission for edits.
type WriteAccess struct {
	mu       sync.Mutex
	cwd      string
	approver WriteApprover
	writable []string
	grants   []writeGrant
	pending  map[string]*writeRequest
	closed   bool
	auto     bool
}

func NewWriteAccess(cfg config.Config, approver WriteApprover) (*WriteAccess, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, err
	}
	root, err := openWriteRoot(cwd)
	if err != nil {
		return nil, err
	}
	a := &WriteAccess{cwd: cwd, approver: approver, auto: cfg.Auto, pending: make(map[string]*writeRequest), grants: []writeGrant{{cwd, root}}}
	for _, dir := range cfg.Writable {
		dir, err = a.canonical(dir)
		if err != nil {
			a.Close()
			return nil, err
		}
		a.writable = append(a.writable, dir)
	}
	return a, nil
}

// SetAuto changes automatic command and folder approval for future requests.
// Pending requests retain their policy; existing session grants are not revoked.
func (a *WriteAccess) SetAuto(enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.auto = enabled
}

// Auto reports whether future commands and outside-folder requests are automatically approved.
func (a *WriteAccess) Auto() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.auto
}

func withinWriteDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (a *WriteAccess) absolute(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.cwd, path)
	}
	return filepath.Clean(path), nil
}

// canonical resolves existing aliases, retaining the suffix of new directories.
func (a *WriteAccess) canonical(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("write directory must not be empty")
	}
	path, err := a.absolute(path)
	if err != nil {
		return "", err
	}
	parent := path
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			info, err := os.Stat(resolved)
			if err != nil {
				return "", err
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", parent)
			}
			rel, _ := filepath.Rel(parent, path)
			return filepath.Join(resolved, rel), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if _, statErr := os.Lstat(parent); statErr == nil {
			return "", err // dangling symlink, not a new directory
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		parent = next
	}
}

// openWriteRoot pins each canonical component, rejecting symlink substitutions
// between resolution and opening. Descendant access then stays inside os.Root.
func openWriteRoot(path string) (*os.Root, error) {
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		before, err := root.Lstat(part)
		if err != nil {
			root.Close()
			return nil, err
		}
		if !before.IsDir() {
			root.Close()
			return nil, fmt.Errorf("write directory changed or is a symlink: %s", path)
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			root.Close()
			return nil, err
		}
		opened, statErr := next.Stat(".")
		after, afterErr := root.Lstat(part)
		root.Close()
		if statErr != nil || afterErr != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
			next.Close()
			return nil, fmt.Errorf("write directory changed while opening: %s", path)
		}
		root = next
	}
	return root, nil
}

func (a *WriteAccess) granted(dir string) bool {
	for _, grant := range a.grants {
		if withinWriteDir(grant.dir, dir) {
			return true
		}
	}
	return false
}

func (a *WriteAccess) Require(ctx context.Context, dirs []string) error {
	return a.requireDirectories(ctx, dirs, false)
}

func (a *WriteAccess) requireBash(ctx context.Context, dirs []string) error {
	return a.requireDirectories(ctx, dirs, true)
}

func (a *WriteAccess) requireDirectories(ctx context.Context, dirs []string, bash bool) error {
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return os.ErrClosed
	}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir, err := a.canonical(dir)
		if err != nil {
			return err
		}
		if bash {
			configured := false
			for _, writable := range a.writable {
				if withinWriteDir(writable, dir) {
					configured = true
					break
				}
			}
			if configured {
				continue // already permitted for bash, not an implicit edit grant
			}
		}
		if err := a.require(ctx, dir); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (a *WriteAccess) require(ctx context.Context, dir string) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return os.ErrClosed
	}
	if a.granted(dir) {
		a.mu.Unlock()
		return ctx.Err()
	}
	if request := a.pending[dir]; request != nil {
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-request.done:
			if err := ctx.Err(); err != nil {
				return err
			}
			return request.err
		}
	}
	request := &writeRequest{done: make(chan struct{})}
	a.pending[dir] = request
	auto := a.auto
	a.mu.Unlock()

	// Pin before approval: approval must not authorize a later symlink target.
	root, err := openWriteRoot(dir)
	if os.IsNotExist(err) {
		err = fmt.Errorf("write directory must exist; request its nearest existing parent: %s", dir)
	}
	if err == nil {
		allowed := auto
		if !allowed && a.approver != nil {
			allowed, err = a.approver.ApproveWrite(ctx, dir)
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if err == nil && !allowed {
			err = fmt.Errorf("write access denied: %s", dir)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed && err == nil {
		err = os.ErrClosed
	}
	if err == nil {
		a.grants = append(a.grants, writeGrant{dir, root})
	} else if root != nil {
		root.Close()
	}
	request.err = err
	delete(a.pending, dir)
	close(request.done)
	return err
}

// SandboxDirectories rejects moved/replaced grants. The sandbox must use these
// canonical names verbatim, never re-resolve symlinks to new authorization.
func (a *WriteAccess) SandboxDirectories() ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, os.ErrClosed
	}
	var dirs []string
	for _, grant := range a.grants {
		if err := validateWriteGrant(grant); err != nil {
			return nil, err
		}
		dirs = append(dirs, grant.dir)
	}
	return append(dirs, a.writable...), nil
}

func validateWriteGrant(grant writeGrant) error {
	current, err := openWriteRoot(grant.dir)
	if err != nil {
		return err
	}
	info, err := current.Stat(".")
	current.Close()
	pinned, pinnedErr := grant.root.Stat(".")
	if err != nil || pinnedErr != nil || !os.SameFile(info, pinned) {
		return fmt.Errorf("approved write directory changed: %s", grant.dir)
	}
	return nil
}

// OpenEditRoot returns a session-owned root; callers must not close it. A path
// already beneath an approved root is never re-resolved outside that root.
func (a *WriteAccess) OpenEditRoot(ctx context.Context, path string) (*os.Root, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	abs, err := a.absolute(path)
	if err != nil {
		return nil, "", err
	}
	if root, rel, found, err := a.editRoot(abs); found || err != nil {
		return root, rel, err
	}
	// Resolve only the directory, not a leaf symlink. os.Root checks the leaf.
	parent := filepath.Dir(abs)
	for {
		info, err := os.Stat(parent)
		if err == nil {
			if !info.IsDir() {
				return nil, "", fmt.Errorf("%s is not a directory", parent)
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, "", err
		}
		if _, statErr := os.Lstat(parent); statErr == nil {
			return nil, "", err
		}
		parent = filepath.Dir(parent)
	}
	dir, err := a.canonical(parent)
	if err != nil {
		return nil, "", err
	}
	rel, _ := filepath.Rel(parent, abs)
	abs = filepath.Join(dir, rel)
	if err := a.Require(ctx, []string{dir}); err != nil {
		return nil, "", err
	}
	root, rel, _, err := a.editRoot(abs)
	return root, rel, err
}

func (a *WriteAccess) editRoot(path string) (*os.Root, string, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, "", false, os.ErrClosed
	}
	for i := range a.grants {
		grant := &a.grants[i]
		if !withinWriteDir(grant.dir, path) {
			continue
		}
		if err := validateWriteGrant(*grant); err != nil {
			return nil, "", true, err
		}
		rel, err := filepath.Rel(grant.dir, path)
		return grant.root, rel, true, err
	}
	// Absolute spellings may include system aliases (/var vs /private/var).
	// Find an approved ancestor before resolving descendants: resolving the
	// whole parent first could turn an escaping symlink into a fresh grant.
	var ancestors []string
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		ancestors = append(ancestors, parent)
		if filepath.Dir(parent) == parent {
			break
		}
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		resolved, err := filepath.EvalSymlinks(ancestors[i])
		if err != nil {
			continue
		}
		suffix, _ := filepath.Rel(ancestors[i], path)
		candidate := filepath.Join(resolved, suffix)
		for _, grant := range a.grants {
			if !withinWriteDir(grant.dir, candidate) {
				continue
			}
			if err := validateWriteGrant(grant); err != nil {
				return nil, "", true, err
			}
			rel, err := filepath.Rel(grant.dir, candidate)
			return grant.root, rel, true, err
		}
	}
	return nil, "", false, nil
}

func (a *WriteAccess) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	var err error
	for _, grant := range a.grants {
		if grant.root != nil {
			err = errors.Join(err, grant.root.Close())
		}
	}
	return err
}
