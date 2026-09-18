//go:build darwin || linux

package daemon

import (
	"os"
	"syscall"
)

func desktopFileOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
