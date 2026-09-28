package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func worktreeDeliverablesReason(ctx context.Context, root string, repositories []string) string {
	if outputContainsFiles(filepath.Join(root, "output")) || hasNonRepositoryFiles(root, repositories) {
		return "output"
	}
	for _, repository := range repositories {
		ignored, err := worktreeGit(ctx, repository, "ls-files", "--others", "--ignored", "--exclude-standard")
		if err != nil {
			return "unavailable"
		}
		if ignored != "" {
			return "output"
		}
	}
	return ""
}

func outputContainsFiles(root string) bool {
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return err != nil || found
}

func artifactContainsRepository(root string) bool {
	found := false
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == ".git" {
			found = true
			return fs.SkipAll
		}
		if entry.Type()&linkedDirModes != 0 && entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	return err != nil || found
}
