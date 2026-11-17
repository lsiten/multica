package execenv

import (
	"errors"
	"os"
	"path/filepath"
)

type RootEntryMove struct {
	Source      *os.Root
	SourceName  string
	Destination *os.Root
	TargetName  string
}

// MoveRootEntryNoReplace keeps both parent directories anchored while the OS
// atomically rejects an existing destination, including on Windows.
func MoveRootEntryNoReplace(move RootEntryMove) error {
	for _, name := range []string{move.SourceName, move.TargetName} {
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
			return errors.New("root entry move requires a basename")
		}
	}
	return renameArchiveDirectoryNoReplace(move.Source, move.SourceName, move.Destination, move.TargetName)
}
