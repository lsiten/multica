//go:build windows

package util

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func readMachineBootID() (string, error) {
	var information struct {
		Identifier   windows.GUID
		FirmwareType uint32
		Padding      uint32
		Flags        uint64
	}
	var size uint32
	if err := windows.NtQuerySystemInformation(windows.SystemBootEnvironmentInformation, unsafe.Pointer(&information), uint32(unsafe.Sizeof(information)), &size); err != nil {
		return "", err
	}
	if information.Identifier == (windows.GUID{}) {
		return "", errors.New("machine boot identifier is empty")
	}
	return information.Identifier.String(), nil
}

// MachineBootTime returns the kernel's recorded start of the current boot.
func MachineBootTime() (time.Time, error) {
	var information struct {
		BootTime      int64
		CurrentTime   int64
		TimeZoneBias  int64
		TimeZoneID    uint32
		Reserved      uint32
		BootTimeBias  uint64
		SleepTimeBias uint64
	}
	var size uint32
	if err := windows.NtQuerySystemInformation(windows.SystemTimeOfDayInformation, unsafe.Pointer(&information), uint32(unsafe.Sizeof(information)), &size); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, (information.BootTime-116444736000000000)*100), nil
}
