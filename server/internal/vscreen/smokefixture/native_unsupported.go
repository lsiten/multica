//go:build !darwin || !cgo

package smokefixture

func register(string) error            { return ErrUnsupported }
func processStart(int) (string, error) { return "", ErrUnsupported }
func run([]byte) error                 { return ErrUnsupported }
func snapshot() (Foreground, error)    { return Foreground{}, ErrUnsupported }
