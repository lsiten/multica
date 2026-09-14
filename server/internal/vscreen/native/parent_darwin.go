//go:build darwin

package native

import (
	"golang.org/x/sys/unix"
	"os"
)

func validateParent(socket, bootstrap *os.File) error {
	info, err := bootstrap.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return ErrProtocol
	}
	credentials, err := unix.GetsockoptXucred(int(socket.Fd()), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return err
	}
	if credentials.Uid != uint32(os.Getuid()) {
		return ErrProtocol
	}
	return nil
}

func prepareDescriptors() error { return unix.SetNonblock(4, true) }
