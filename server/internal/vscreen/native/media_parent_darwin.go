//go:build darwin

package native

import (
	"crypto/subtle"
	"io"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func openMediaParent(token [32]byte) (net.Conn, error) {
	var controlStat, mediaStat unix.Stat_t
	if unix.Fstat(3, &controlStat) != nil || unix.Fstat(5, &mediaStat) != nil {
		return nil, ErrProtocol
	}
	if controlStat.Dev == mediaStat.Dev && controlStat.Ino == mediaStat.Ino {
		return nil, ErrProtocol
	}
	credentials, err := unix.GetsockoptXucred(5, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil || credentials.Uid != uint32(os.Getuid()) {
		return nil, ErrProtocol
	}
	file := os.NewFile(5, "vscreen-media")
	defer file.Close()
	connection, err := net.FileConn(file)
	if err != nil {
		return nil, err
	}
	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		connection.Close()
		return nil, err
	}
	var provided [32]byte
	if _, err := io.ReadFull(connection, provided[:]); err != nil {
		connection.Close()
		return nil, err
	}
	authenticated := subtle.ConstantTimeCompare(token[:], provided[:]) == 1
	clear(provided[:])
	if !authenticated {
		connection.Close()
		return nil, ErrProtocol
	}
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		connection.Close()
		return nil, err
	}
	return connection, nil
}
