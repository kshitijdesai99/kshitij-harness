//go:build linux

package ecosystem

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func ProcessForPID(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, ErrIdentity
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ProcessIdentity{}, err
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ProcessIdentity{}, err
	}
	// comm may contain spaces and parentheses; the last ')' closes field 2.
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return ProcessIdentity{}, ErrIdentity
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 {
		return ProcessIdentity{}, ErrIdentity
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return ProcessIdentity{}, ErrIdentity
	}
	return ProcessIdentity{PID: pid, StartToken: fmt.Sprintf("linux:%s:%d", strings.TrimSpace(string(boot)), ticks)}, nil
}
