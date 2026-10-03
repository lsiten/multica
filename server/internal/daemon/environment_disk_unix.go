//go:build darwin || linux

package daemon

import "syscall"

func environmentFreeBytes(path string) (*uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}
	value := uint64(stat.Bavail) * uint64(stat.Bsize)
	return &value, nil
}
