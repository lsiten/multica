//go:build windows

package applicationhost

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func readBootID() (string, error) {
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
