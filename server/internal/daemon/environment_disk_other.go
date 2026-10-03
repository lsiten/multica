//go:build !darwin && !linux && !windows

package daemon

func environmentFreeBytes(string) (*uint64, error) { return nil, nil }
