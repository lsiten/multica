//go:build !darwin || !cgo

package smokefixture

func runQualification([]byte) error    { return ErrUnsupported }
func QualificationInputSource() string { return "" }

func QualificationCodeHash(string) string { return "" }
