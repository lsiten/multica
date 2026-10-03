//go:build windows

package daemon

import "golang.org/x/sys/windows"

func environmentFreeBytes(path string) (*uint64, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(pointer, &available, &total, &free); err != nil {
		return nil, err
	}
	return &available, nil
}
