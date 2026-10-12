//go:build windows

package daemon

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

// lockReportOutboxDomain takes a non-blocking exclusive lock on the file that
// names an outbox namespace. LOCKFILE_FAIL_IMMEDIATELY makes the request fail
// instead of blocking when another process holds the lock; that failure is the
// owned verdict that lets control's replay loops defer to the gateway. A
// reparse point is rejected so the single-owner guarantee cannot be bypassed by
// a symlink or directory.
func lockReportOutboxDomain(path string) (*os.File, bool, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(handle), path)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		file.Close()
		return nil, false, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		file.Close()
		return nil, false, errors.New("report outbox lock must be a regular file without redirection")
	}
	var overlap windows.Overlapped
	if err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap); err != nil {
		// Only a lock violation proves another owner; storage failures retain
		// their error so callers can retry without bypassing the ownership fence.
		file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return file, false, nil
}
