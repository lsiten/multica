//go:build darwin || linux

package smokefixture

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

func qualificationReadFile(path string, limit int64, private bool) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "qualification-owned-file")
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	var native unix.Stat_t
	if unix.Fstat(fd, &native) != nil || !stat.Mode().IsRegular() || native.Nlink != 1 || stat.Size() > limit || private && (native.Uid != uint32(os.Getuid()) || stat.Mode().Perm()&0077 != 0) {
		return nil, errors.New("unsafe_qualification_file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(raw)) > limit {
		return nil, errors.New("unsafe_qualification_file")
	}
	return raw, err
}
func qualificationOwnedDirectory(path string) bool {
	var stat unix.Stat_t
	return unix.Lstat(path, &stat) == nil && stat.Mode&unix.S_IFMT == unix.S_IFDIR && stat.Mode&0777 == 0700 && stat.Uid == uint32(os.Getuid())
}
