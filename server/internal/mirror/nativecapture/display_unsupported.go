//go:build !darwin || !cgo

package nativecapture

func listDisplays() ([]Display, error) { return nil, ErrUnsupported }
func supported() bool                  { return false }
func runLoop()                         {}
func stopLoop()                        {}
