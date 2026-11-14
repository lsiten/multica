//go:build !darwin && !linux && !windows

package util

import (
	"errors"
	"time"
)

func readMachineBootID() (string, error) {
	return "", errors.New("machine boot identity is unavailable on this platform")
}

// MachineBootTime is unavailable on unsupported platforms.
func MachineBootTime() (time.Time, error) {
	return time.Time{}, errors.New("machine boot time is unavailable on this platform")
}
