//go:build darwin || linux

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

func cacheFileIdentity(info os.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	}
	return info.Name()
}

func worktreeFileAllocation(info os.FileInfo) (string, int64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, false
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), stat.Blocks * 512, true
}
