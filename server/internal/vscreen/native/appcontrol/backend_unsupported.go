//go:build !darwin || !cgo

package appcontrol

func newBackend() (backend, error) { return nil, refusal("unsupported_platform") }
