//go:build !windows

package applicationhost

import (
	"errors"
	"os"
	"syscall"
)

func protectPrivateFile(file *os.File) error { return file.Chmod(0600) }

func validatePrivateFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("application host file must be private and owned by the current user")
	}
	return nil
}
