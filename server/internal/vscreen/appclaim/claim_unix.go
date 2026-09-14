//go:build darwin || linux

package appclaim

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func openLocked(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("appclaim open: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err == nil && (stat.Uid != uint32(os.Getuid()) || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0077 != 0) {
		err = fmt.Errorf("appclaim: insecure lockfile")
	}
	if err == nil {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("appclaim lock: %w; close: %v", err, closeErr)
		}
		return nil, fmt.Errorf("appclaim lock: %w", err)
	}
	return file, nil
}
