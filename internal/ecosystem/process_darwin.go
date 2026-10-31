//go:build darwin

package ecosystem

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ProcessForPID uses kernel microsecond start time, not ps's second-resolution
// display. Including the boot-session UUID prevents a registry surviving reboot from
// accidentally matching a new process incarnation.
func ProcessForPID(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, ErrIdentity
	}
	ps, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	if len(ps) == 0 {
		return ProcessIdentity{}, os.ErrNotExist
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ProcessIdentity{}, err
	}
	start := ps[0].Proc.P_starttime
	if start.Sec == 0 {
		return ProcessIdentity{}, ErrIdentity
	}
	return ProcessIdentity{PID: pid, StartToken: fmt.Sprintf("darwin:%s:%d:%d", boot, start.Sec, start.Usec)}, nil
}
