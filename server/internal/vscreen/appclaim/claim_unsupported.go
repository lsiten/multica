//go:build !darwin && !linux

package appclaim

import (
	"fmt"
	"os"
)

func openLocked(string) (*os.File, error) { return nil, fmt.Errorf("appclaim: unsupported platform") }
