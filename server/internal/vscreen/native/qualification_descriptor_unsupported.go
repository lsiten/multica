//go:build !darwin && !linux

package native

import "os"

func qualificationPollablePipe(*os.File) (*os.File, error) { return nil, ErrUnsupported }
