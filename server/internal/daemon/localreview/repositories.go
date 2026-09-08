package localreview

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Repositories discovers checked-out roots under an authorized task directory.
// Traversal is bounded and never follows symlinked directories.
func Repositories(ctx context.Context, path string) ([]string, error) {
	result := []string{}
	count := 0
	err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		count++
		if count > 10000 {
			return errors.New("too many entries; select a repository directory")
		}
		if !entry.IsDir() {
			return nil
		}
		if entry.Name() == "node_modules" || entry.Name() == ".git" {
			return fs.SkipDir
		}
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			result = append(result, current)
			return fs.SkipDir
		}
		rel, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		if strings.Count(filepath.ToSlash(rel), "/") >= 3 {
			return fs.SkipDir
		}
		return nil
	})
	return result, err
}
