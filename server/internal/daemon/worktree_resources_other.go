//go:build !darwin && !linux

package daemon

import "os"

func cacheFileIdentity(info os.FileInfo) string {
	return info.Name()
}

func worktreeFileAllocation(info os.FileInfo) (string, int64, bool) {
	return "", 0, false
}
