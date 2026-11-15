//go:build !darwin

package nativecapture

import "net"

func openMediaParent([32]byte) (net.Conn, error) {
	return nil, ErrUnsupported
}
