//go:build !windows

package analytics

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func lockAnalyticsOutbox(dir string) (*os.File, error) {
	if err := ensureAnalyticsDirectory(dir); err != nil {
		return nil, err
	}
	fd, err := unix.Open(dir+"/.upload.lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), dir+"/.upload.lock")
	if err := validateAnalyticsFile(file); err != nil {
		file.Close()
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}
	return file, nil
}

func ensureAnalyticsDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Getuid()) {
		return errors.New("analytics outbox must be private and owned")
	}
	return nil
}

func validateAnalyticsFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Getuid()) {
		return errors.New("analytics queue file must be private and owned")
	}
	return nil
}

func syncAnalyticsDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
