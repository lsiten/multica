//go:build !windows

package daemon

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

// lockReportOutboxDomain takes a non-blocking exclusive flock on the file that
// names an outbox namespace. It is the kernel boundary of the "one uploader per
// namespace" rule: only the process that holds it may upload that namespace's
// terminal and JEV reports. When another process already holds the lock, the
// non-blocking request fails with EWOULDBLOCK and the call reports owned=true
// instead of blocking, so control's replay loops defer to the gateway.
func lockReportOutboxDomain(path string) (*os.File, bool, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		file.Close()
		return nil, false, errors.New("report outbox lock must be private")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return file, false, nil
}
