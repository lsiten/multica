//go:build !darwin || !cgo

package native

func createDisplay(string, uint32, uint32, uint32) (Display, error) { return Display{}, ErrUnsupported }
func describeDisplay(uint32) (Display, error)                       { return Display{}, ErrUnsupported }
func listDisplays() ([]Display, error)                              { return nil, ErrUnsupported }
func disposeDisplay(uint32) error                                   { return ErrUnsupported }
func supported() bool                                               { return false }
func runLoop()                                                      {}
func stopLoop()                                                     {}
