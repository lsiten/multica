//go:build darwin

package util

import (
	"golang.org/x/sys/unix"
	"time"
)

func readMachineBootID() (string, error) { return unix.Sysctl("kern.bootsessionuuid") }

// MachineBootTime returns the kernel's recorded start of the current boot.
func MachineBootTime() (time.Time, error) {
	value, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(value.Sec, int64(value.Usec)*1000), nil
}
