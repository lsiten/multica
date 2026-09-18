//go:build !darwin && !linux

package smokefixture

func qualificationReadFile(string, int64, bool) ([]byte, error) { return nil, ErrUnsupported }
func qualificationOwnedDirectory(string) bool                   { return false }
