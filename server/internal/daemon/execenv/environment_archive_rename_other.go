//go:build !linux && !darwin && !windows

package execenv

import (
	"errors"
	"os"
)

func renameArchiveDirectoryNoReplace(source *os.Root, from string, destination *os.Root, to string) error {
	return errors.New("atomic archive publication is unavailable on this platform")
}
