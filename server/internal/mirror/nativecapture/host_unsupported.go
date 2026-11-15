//go:build !darwin

package nativecapture

func RunHost(string) error { return ErrUnsupported }
