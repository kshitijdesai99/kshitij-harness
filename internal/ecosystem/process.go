package ecosystem

import (
	"errors"
	"os"
	"path/filepath"
)

func Path() string                             { home, _ := os.UserHomeDir(); return filepath.Join(home, ".kh", "ecosystem.db") }
func CurrentProcess() (ProcessIdentity, error) { return ProcessForPID(os.Getpid()) }

// ProbeProcess compares the PID AND kernel start identity. An unrelated process
// reusing the PID is evidence that the original incarnation has exited.
func ProbeProcess(p ProcessIdentity) (bool, error) {
	if !p.Valid() {
		return false, ErrIdentity
	}
	current, err := ProcessForPID(p.PID)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current == p, nil
}
