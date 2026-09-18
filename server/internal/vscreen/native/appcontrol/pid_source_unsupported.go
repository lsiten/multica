//go:build !darwin || !cgo

package appcontrol

func currentPIDInputSource() string { return "" }
