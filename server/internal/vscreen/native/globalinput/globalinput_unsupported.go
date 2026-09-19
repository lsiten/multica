//go:build !windows && !(cgo && darwin)

package globalinput

// New returns the unavailable injector for builds without global input
// support (notably Linux and CGO-free macOS builds).
func New() Injector { return unsupportedInjector{} }

type unsupportedInjector struct{}

func (unsupportedInjector) Available() bool                       { return false }
func (unsupportedInjector) Pointer(PointerEvent) error            { return ErrUnsupported }
func (unsupportedInjector) Key(KeyEvent) error                    { return ErrUnsupported }
func (unsupportedInjector) Text(string) error                     { return ErrUnsupported }
func (unsupportedInjector) Close() error                          { return nil }
