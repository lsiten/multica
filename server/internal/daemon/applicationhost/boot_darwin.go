//go:build darwin

package applicationhost

import "golang.org/x/sys/unix"

func readBootID() (string, error) { return unix.Sysctl("kern.bootsessionuuid") }
