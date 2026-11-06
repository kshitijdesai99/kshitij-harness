package projectconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
)

// FoundationSchemaVersion changes whenever discovery or snapshot semantics change.
const FoundationSchemaVersion = 1

// FoundationRef identifies inert project context, not authority to execute any
// discovered script or start an MCP connection. IDs are opaque source digests.
type FoundationRef struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Directory     string `json:"directory"`
}

// FoundationSnapshot is an assignment-boundary view. Config and every mutable
// map/slice it contains belong exclusively to the caller. Ref alone is not prompt
// content: callers must still deliver Config.Prompt to fresh provider histories.
type FoundationSnapshot struct {
	Ref    FoundationRef
	Config *Config
	Hit    bool
	Reason string
}

// FoundationCache keeps at most 32 directory snapshots in this process. It does
// not persist files (in particular, never Config.Servers credentials). Workers
// in separate processes can compare Ref IDs but do not physically share data.
// The zero value is ready to use. Resolve serializes discovery and validation;
// no task-specific closures, provider histories, or tool permissions are cached.
type FoundationCache struct {
	mu      sync.Mutex
	entries map[string]foundationEntry
}

type foundationEntry struct {
	ref    FoundationRef
	config *Config
	trace  *sourceTrace
}

var defaultFoundationCache FoundationCache

func NewFoundationCache() *FoundationCache { return &FoundationCache{} }

// Resolve validates filesystem content at each call, including same-size edits
// with preserved timestamps, additions/deletions and symlink changes. Warm hits
// avoid parsing and catalog construction, not validation I/O. Supporting asset
// bodies remain lazy and are safely reread by capability load/read as before.
func Resolve(ctx context.Context, directory string) (FoundationSnapshot, error) {
	return defaultFoundationCache.Resolve(ctx, directory)
}

func (c *FoundationCache) Resolve(ctx context.Context, directory string) (FoundationSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return FoundationSnapshot{}, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return FoundationSnapshot{}, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return FoundationSnapshot{}, fmt.Errorf("project config cwd: %w", err)
	}
	// HOME can change independently of the directory (including in tests).
	home, _ := os.UserHomeDir()
	resolvedHome, _ := filepath.EvalSymlinks(home)
	key := absolute + "\x00" + home + "\x00" + resolvedHome
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return FoundationSnapshot{}, err
	}
	reason := "cold"
	if entry, ok := c.entries[key]; ok {
		if entry.ref.SchemaVersion == FoundationSchemaVersion && entry.trace.valid(ctx) {
			return FoundationSnapshot{entry.ref, cloneConfig(entry.config), true, "validated"}, nil
		}
		delete(c.entries, key)
		reason = "sources_changed"
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return FoundationSnapshot{}, err
		}
		trace := &sourceTrace{sources: make(map[sourceKey]string)}
		config, err := loadConfig(absolute, trace)
		if err != nil {
			return FoundationSnapshot{}, err
		}
		if err := ctx.Err(); err != nil {
			return FoundationSnapshot{}, err
		}
		if !trace.uncacheable && !trace.valid(ctx) {
			continue
		}
		ref := FoundationRef{FoundationSchemaVersion, trace.id(key), absolute}
		if trace.uncacheable {
			return FoundationSnapshot{ref, config, false, "uncacheable_sources"}, nil
		}
		if c.entries == nil {
			c.entries = make(map[string]foundationEntry)
		}
		if len(c.entries) >= 32 {
			// Bounded retention without a second, mutable LRU bookkeeping layer.
			c.entries = make(map[string]foundationEntry)
		}
		c.entries[key] = foundationEntry{ref, config, trace}
		return FoundationSnapshot{ref, cloneConfig(config), false, reason}, nil
	}
	return FoundationSnapshot{}, fmt.Errorf("project configuration changed during discovery; retry assignment")
}

func cloneMap[V any](in map[string]V) map[string]V {
	if in == nil {
		return nil
	}
	out := make(map[string]V, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneConfig(c *Config) *Config {
	out := *c
	out.Commands = cloneMap(c.Commands)
	out.CommandDirs = cloneMap(c.CommandDirs)
	out.Warnings = append([]string(nil), c.Warnings...)
	out.Hooks = append([]Hook(nil), c.Hooks...)
	for i := range out.Hooks {
		out.Hooks[i].Args = append([]string(nil), c.Hooks[i].Args...)
		out.Hooks[i].Env = cloneMap(c.Hooks[i].Env)
	}
	out.commandPaths = cloneMap(c.commandPaths)
	out.commandRoots = cloneMap(c.commandRoots)
	out.assets = cloneMap(c.assets)
	out.Servers = cloneMap(c.Servers)
	for key, server := range out.Servers {
		server.Args = append([]string(nil), server.Args...)
		server.Env = cloneMap(server.Env)
		server.Headers = cloneMap(server.Headers)
		out.Servers[key] = server
	}
	return &out
}

// sourceTrace records dependencies in the loader itself so there is no second
// discovery implementation to drift from home/nested/plugin precedence rules.
// Only fingerprints, never source bytes, are retained in this manifest.
type sourceKey struct{ Path, Kind string }
type sourceTrace struct {
	sources     map[sourceKey]string
	uncacheable bool
	changed     bool
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (t *sourceTrace) record(key sourceKey, value string) {
	if old, ok := t.sources[key]; ok && old != value {
		t.changed = true
	}
	t.sources[key] = value
}

func statFingerprint(info os.FileInfo, err error, path string) (string, bool) {
	if os.IsNotExist(err) {
		return "missing", true
	}
	if err != nil {
		return "", false
	}
	value := fmt.Sprint(info.Mode())
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", false
		}
		value += ":" + target
	}
	return value, true
}

func (t *sourceTrace) stat(path string) {
	// Track all ancestors as well: replacing a parent with a symlink must
	// invalidate even when the final file has identical content and mode.
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		value, ok := statFingerprint(info, err, current)
		if !ok {
			t.uncacheable = true
		}
		t.record(sourceKey{current, "stat"}, value)
		if filepath.Dir(current) == current {
			break
		}
	}
}

func (l *loader) lstat(path string) (os.FileInfo, error) {
	if l.trace != nil {
		l.trace.stat(path)
	}
	return os.Lstat(path)
}

func (l *loader) safePath(path, root string) error {
	if l.trace != nil {
		l.trace.stat(path)
	}
	return safeAssetPath(path, root)
}

func directoryFingerprint(entries []os.DirEntry) string {
	type member struct {
		Name string
		Type fs.FileMode
	}
	members := make([]member, 0, len(entries))
	for _, entry := range entries {
		members = append(members, member{entry.Name(), entry.Type()})
	}
	data, _ := json.Marshal(members)
	return digest(data)
}

func (l *loader) readDir(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if l.trace != nil {
		l.trace.stat(path)
		if err != nil {
			l.trace.uncacheable = true
		} else {
			l.trace.record(sourceKey{path, "directory"}, directoryFingerprint(entries))
		}
	}
	return entries, err
}

func (l *loader) walkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if l.trace != nil {
			l.trace.stat(path)
		}
		result := fn(path, entry, err)
		// WalkDir will enumerate precisely directories not skipped by fn.
		// Do not enumerate ignored runtime state or open supporting assets.
		if l.trace != nil && err == nil && entry.IsDir() && result == nil {
			_, _ = l.readDir(path)
		}
		return result
	})
}

func (t *sourceTrace) recordRead(path string, data []byte, err error) {
	if err != nil {
		t.uncacheable = true
		return
	}
	t.record(sourceKey{path, "read"}, digest(data))
}

func (t *sourceTrace) valid(ctx context.Context) bool {
	if t == nil || t.uncacheable || t.changed {
		return false
	}
	for key, expected := range t.sources {
		if ctx.Err() != nil {
			return false
		}
		var actual string
		switch key.Kind {
		case "stat":
			info, err := os.Lstat(key.Path)
			value, ok := statFingerprint(info, err, key.Path)
			if !ok {
				return false
			}
			actual = value
		case "directory":
			entries, err := os.ReadDir(key.Path)
			if err != nil {
				return false
			}
			actual = directoryFingerprint(entries)
		case "read":
			// Recheck every component before bounded open; symlink swaps must
			// never cause the validator to read an unrelated target's bytes.
			if err := safeAssetPath(key.Path, filepath.Dir(key.Path)); err != nil {
				return false
			}
			file, err := openRegularSource(key.Path)
			if err != nil {
				return false
			}
			data, err := io.ReadAll(io.LimitReader(file, maxFile+1))
			file.Close()
			if err != nil {
				return false
			}
			actual = digest(data)
		default:
			return false
		}
		if actual != expected {
			return false
		}
	}
	return true
}

// openRegularSource must not block if a regular dependency is replaced by a
// FIFO between validation and open. O_NOFOLLOW also closes the leaf-symlink
// race; fstat verifies the opened descriptor, not stale path metadata. Project
// discovery supports Unix, as does the terminal/process runtime.
func openRegularSource(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("source is not a regular file")
	}
	return file, nil
}

func (t *sourceTrace) id(key string) string {
	keys := make([]sourceKey, 0, len(t.sources))
	for source := range t.sources {
		keys = append(keys, source)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Path == keys[j].Path {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].Path < keys[j].Path
	})
	hash := sha256.New()
	fmt.Fprintf(hash, "foundation:%d:%s\n", FoundationSchemaVersion, key)
	for _, source := range keys {
		fmt.Fprintf(hash, "%q:%q:%s\n", source.Path, source.Kind, t.sources[source])
	}
	return hex.EncodeToString(hash.Sum(nil))
}
