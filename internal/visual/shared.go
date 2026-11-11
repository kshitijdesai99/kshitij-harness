package visual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"kh/internal/ecosystem"
)

// Shared state is private to one exact root process incarnation. A file lock
// serializes launches and shutdown across worker clients; manifests are atomic
// and stop requests never send signals to stored/reusable process IDs.
type sharedServer struct {
	Server   Server                    `json:"server"`
	Guardian ecosystem.ProcessIdentity `json:"guardian"`
	Stop     string                    `json:"stop"`
}

func (m *Manager) lockShared(ctx context.Context) (func(), error) {
	file, err := os.OpenFile(filepath.Join(m.root, "state", "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	// Bound even callers without a cancellation context. In particular a
	// stalled dependency install must not indefinitely block list/stop.
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}

func (m *Manager) checkOwner() error {
	if _, err := os.Stat(filepath.Join(m.root, "state", "closed")); !os.IsNotExist(err) {
		return fmt.Errorf("visual root session is closed")
	}
	alive, err := ecosystem.ProbeProcess(m.options.Owner)
	if err != nil {
		return err
	}
	if !alive {
		return fmt.Errorf("visual root session has exited")
	}
	return nil
}

func (m *Manager) manifest(name string) string {
	return filepath.Join(m.root, "state", name+".json")
}
func (m *Manager) readShared(name string) (sharedServer, error) {
	var item sharedServer
	if !validName.MatchString(name) {
		return item, fmt.Errorf("invalid visual name")
	}
	data, err := os.ReadFile(m.manifest(name))
	if err != nil {
		return item, err
	}
	err = json.Unmarshal(data, &item)
	return item, err
}
func sharedRunning(item sharedServer) bool {
	if _, err := os.Stat(item.Stop + ".done"); err == nil {
		return false
	}
	alive, err := ecosystem.ProbeProcess(item.Guardian)
	// A failed probe is not proof of exit; never start a duplicate on uncertainty.
	return err != nil || alive
}
func (m *Manager) writeShared(item sharedServer) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Join(m.root, "state"), ".manifest-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), m.manifest(item.Server.Name))
}
func (m *Manager) listShared() []Server {
	result := []Server{}
	paths, _ := filepath.Glob(filepath.Join(m.root, "state", "*.json"))
	for _, path := range paths {
		name := filepath.Base(path)
		item, err := m.readShared(name[:len(name)-len(".json")])
		if err != nil {
			continue
		}
		server := item.Server
		server.Running = sharedRunning(item)
		result = append(result, server)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
func (m *Manager) stopShared(name string) error {
	item, err := m.readShared(name)
	if err != nil {
		return fmt.Errorf("no owned server named %q: %w", name, err)
	}
	if sharedRunning(item) {
		if err := os.WriteFile(item.Stop, nil, 0600); err != nil {
			return err
		}
		deadline := time.Now().Add(8 * time.Second)
		for sharedRunning(item) {
			if time.Now().After(deadline) {
				return fmt.Errorf("visual guardian did not stop for %q", name)
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	return os.Remove(m.manifest(name))
}
