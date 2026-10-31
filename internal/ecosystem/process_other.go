//go:build !linux && !darwin

package ecosystem

import "errors"

func ProcessForPID(pid int) (ProcessIdentity, error) {
	return ProcessIdentity{}, errors.New("exact process identity unavailable on this platform")
}
