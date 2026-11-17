package workspacefiles

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

const recordsPath = "file-operations"

func checkParents(root *os.Root, path string) error {
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		info, err := root.Lstat(strings.Join(parts[:i], "/"))
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsupportedEntry
		}
	}
	return nil
}

func regularEntry(root *os.Root, path string) (os.FileInfo, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil, ErrUnsupportedEntry
	}
	return info, nil
}

func openRecords(root *os.Root) (*os.Root, error) {
	for _, name := range []string{recordsPath} {
		if err := root.Mkdir(name, 0o700); err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrUnsupportedEntry
		}
	}
	return root.OpenRoot(recordsPath)
}

type entryMove struct {
	sourceRoot, destinationRoot *os.Root
	source, destination         string
}

func moveEntry(move entryMove) error {
	sourceParent, err := move.sourceRoot.OpenRoot(filepath.Dir(move.source))
	if err != nil {
		return err
	}
	defer sourceParent.Close()
	destinationParent, err := move.destinationRoot.OpenRoot(filepath.Dir(move.destination))
	if err != nil {
		return err
	}
	defer destinationParent.Close()
	return execenv.MoveRootEntryNoReplace(execenv.RootEntryMove{Source: sourceParent, SourceName: filepath.Base(move.source), Destination: destinationParent, TargetName: filepath.Base(move.destination)})
}
