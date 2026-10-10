//go:build !windows

package modelservice

import "os"

func replaceStateFile(from, to string) error { return os.Rename(from, to) }
