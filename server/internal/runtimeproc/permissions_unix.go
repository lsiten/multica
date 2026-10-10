//go:build !windows

package runtimeproc

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
		return errors.New("runtime file must be private and owned by the current user")
	}
	return nil
}

func validateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("runtime directory must be private and owned by the current user")
	}
	return nil
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func replaceFile(from, to string) error    { return os.Rename(from, to) }
func secureNewDirectory(path string) error { return checkDirectory(path) }
