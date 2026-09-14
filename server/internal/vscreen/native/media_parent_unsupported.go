//go:build !darwin

package native

import "net"

func openMediaParent([32]byte) (net.Conn, error) { return nil, ErrUnsupported }
