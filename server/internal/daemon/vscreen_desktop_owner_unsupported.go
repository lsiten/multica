//go:build !darwin && !linux

package daemon

import "os"

func desktopFileOwned(os.FileInfo) bool { return false }
