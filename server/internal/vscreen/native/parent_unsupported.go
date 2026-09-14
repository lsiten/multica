//go:build !darwin

package native

import "os"

func validateParent(*os.File, *os.File) error { return ErrUnsupported }

func prepareDescriptors() error { return ErrUnsupported }
