package projectconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

var hookTrustMu sync.Mutex
var hookTrustIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// HookTrustPath is the user-owned trust store, never project-controlled settings.
func HookTrustPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kh", "plugin-trust.json"), nil
}

// LoadHookTrust reads persisted explicit plugin grants. Missing stores are empty;
// malformed, oversized or symlinked stores fail closed. Discovery never calls it.
func LoadHookTrust(path string) (map[string]bool, error) {
	grants := map[string]bool{}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return grants, nil
	}
	// Canonicalize the parent to allow normal OS aliases such as macOS /var;
	// never resolve the leaf, so a symlink cannot become a trust-store target.
	directory, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	path = filepath.Join(directory, filepath.Base(path))
	if err := safeAssetPath(path, directory); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("plugin trust store too large")
	}
	var store struct {
		Version int             `json:"version"`
		Plugins map[string]bool `json:"plugins"`
	}
	if json.Unmarshal(data, &store) != nil || store.Version != 1 || store.Plugins == nil {
		return nil, fmt.Errorf("invalid plugin trust store")
	}
	for id, value := range store.Plugins {
		if !hookTrustIDPattern.MatchString(id) {
			return nil, fmt.Errorf("invalid plugin trust fingerprint")
		}
		if value {
			grants[id] = true
		}
	}
	return grants, nil
}

// SetHookTrust persists an explicit user decision. Callers must obtain that
// decision independently of auto approval and enabledPlugins. Revocation removes
// the fingerprint. Updates are atomic within this process; grants contain no secrets.
func SetHookTrust(path, id string, trusted bool) error {
	if !hookTrustIDPattern.MatchString(id) {
		return fmt.Errorf("invalid plugin trust fingerprint")
	}
	hookTrustMu.Lock()
	defer hookTrustMu.Unlock()
	grants, err := LoadHookTrust(path)
	if err != nil {
		return err
	}
	if trusted {
		grants[id] = true
	} else {
		delete(grants, id)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	path = filepath.Join(directory, filepath.Base(path))
	if err := safeAssetPath(directory, directory); err != nil {
		return err
	}
	data, err := json.MarshalIndent(struct {
		Version int             `json:"version"`
		Plugins map[string]bool `json:"plugins"`
	}{1, grants}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxFile {
		return fmt.Errorf("plugin trust store too large")
	}
	f, err := os.CreateTemp(directory, ".plugin-trust-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
