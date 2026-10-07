package execenv

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// PruneUnusedSharedDirectory removes a caller-owned tree only after excluding live users and pending deliveries.
func PruneUnusedSharedDirectory(ctx context.Context, path string) (bool, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	if filepath.Clean(canonical) != filepath.Clean(path) {
		return false, errors.New("shared directory cleanup path must be canonical")
	}
	stateDir, err := sharedDirectoryStateDir(canonical)
	if err != nil {
		return false, err
	}
	admit, err := lockSharedDirectoryState(ctx, filepath.Join(filepath.Dir(stateDir), ".admission"))
	if err != nil {
		return false, err
	}
	admitted := true
	defer func() {
		if admitted {
			admit()
		}
	}()
	var protected bool
	err = filepath.WalkDir(canonical, func(directory string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		busy, err := SharedDirectoryUnsettled(ctx, directory)
		if err != nil {
			return err
		}
		if busy {
			protected = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil || protected {
		return false, err
	}
	trash, err := os.MkdirTemp(filepath.Dir(canonical), ".application-gc-")
	if err != nil {
		return false, err
	}
	if err := os.Remove(trash); err != nil {
		return false, err
	}
	if err := os.Rename(canonical, trash); err != nil {
		return false, err
	}
	// Re-checking the original path during admission prevents new borrowers entering the renamed tree.
	admit()
	admitted = false
	return true, os.RemoveAll(trash)
}
