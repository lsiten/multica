//go:build darwin

package execenv

import (
	"golang.org/x/sys/unix"
	"os"
)

func renameArchiveDirectoryNoReplace(source *os.Root, from string, destination *os.Root, to string) error {
	sourceDirectory, err := source.Open(".")
	if err != nil {
		return err
	}
	defer sourceDirectory.Close()
	destinationDirectory, err := destination.Open(".")
	if err != nil {
		return err
	}
	defer destinationDirectory.Close()
	return unix.RenameatxNp(int(sourceDirectory.Fd()), from, int(destinationDirectory.Fd()), to, unix.RENAME_EXCL)
}
