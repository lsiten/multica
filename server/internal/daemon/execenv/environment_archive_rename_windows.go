//go:build windows

package execenv

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func renameArchiveDirectoryNoReplace(source *os.Root, from string, destination *os.Root, to string) error {
	sourceName, err := windows.UTF16PtrFromString(filepath.Join(source.Name(), from))
	if err != nil {
		return err
	}
	destinationName, err := windows.UTF16PtrFromString(filepath.Join(destination.Name(), to))
	if err != nil {
		return err
	}
	return windows.MoveFile(sourceName, destinationName)
}
