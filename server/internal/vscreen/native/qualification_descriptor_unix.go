//go:build darwin || linux

package native

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func qualificationPollablePipe(file *os.File) (*os.File, error) {
	syscall.ForkLock.Lock()
	fd, err := unix.Dup(int(file.Fd()))
	if err == nil {
		unix.CloseOnExec(fd)
	}
	syscall.ForkLock.Unlock()
	if err != nil {
		return nil, err
	}
	if err = unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), "bounded-qualification-pipe"), nil
}
