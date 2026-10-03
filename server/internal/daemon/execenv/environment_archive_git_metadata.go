package execenv

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var archivedGitMetadataPaths = []string{
	"shallow", "info/exclude", "info/attributes", "info/sparse-checkout", "logs",
	"ORIG_HEAD", "MERGE_HEAD", "MERGE_MSG", "MERGE_MODE", "AUTO_MERGE", "CHERRY_PICK_HEAD", "REVERT_HEAD",
	"SQUASH_MSG", "COMMIT_EDITMSG", "FETCH_HEAD", "sequencer", "rebase-merge", "rebase-apply",
}

// Operation state and reflogs make conflict, cherry-pick and rebase recovery
// usable. Index locks, hooks, alternates and absolute worktree pointers are not
// replayed into the reconstructed repository.
func captureArchivedGitMetadata(ctx context.Context, common, private, recovery string, oidLength int) ([]string, error) {
	objects := make(map[string]bool)
	directories := []string{common}
	if private != common {
		directories = append(directories, private)
	}
	for _, directory := range directories {
		source, err := os.OpenRoot(directory)
		if err != nil {
			return nil, err
		}
		for _, name := range archivedGitMetadataPaths {
			err := fs.WalkDir(source.FS(), name, func(path string, entry fs.DirEntry, walkErr error) error {
				if errors.Is(walkErr, os.ErrNotExist) && path == name {
					return nil
				}
				if walkErr != nil {
					return walkErr
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
					return errors.New("git recovery metadata is a filesystem link")
				}
				if entry.IsDir() {
					return nil
				}
				if !entry.Type().IsRegular() {
					return errors.New("unsupported git recovery metadata")
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Size() > 12<<20 {
					return errors.New("git recovery metadata exceeds archive limit")
				}
				file, err := source.Open(filepath.FromSlash(path))
				if err != nil {
					return err
				}
				data, readErr := io.ReadAll(io.LimitReader(archiveContextReader{ctx, file}, (12<<20)+1))
				if err := errors.Join(readErr, file.Close()); err != nil {
					return err
				}
				if int64(len(data)) != info.Size() {
					return errors.New("git recovery metadata changed")
				}
				target := filepath.Join(recovery, "metadata", filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					return err
				}
				if err := os.WriteFile(target, data, 0600); err != nil {
					return err
				}
				for _, field := range archivedMetadataObjectFields(path, data) {
					if len(field) != oidLength || strings.Trim(field, "0") == "" {
						continue
					}
					if _, err := hex.DecodeString(field); err == nil {
						objects[field] = true
					}
				}
				return nil
			})
			if err != nil {
				source.Close()
				return nil, err
			}
		}
		source.Close()
	}
	result := make([]string, 0, len(objects))
	for oid := range objects {
		result = append(result, oid)
	}
	return result, nil
}

func archivedMetadataObjectFields(path string, data []byte) []string {
	fields := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		if strings.HasPrefix(path, "logs/") {
			if len(words) >= 2 {
				fields = append(fields, words[0], words[1])
			}
			continue
		}
		switch path {
		case "ORIG_HEAD", "MERGE_HEAD", "AUTO_MERGE", "CHERRY_PICK_HEAD", "REVERT_HEAD", "FETCH_HEAD":
			fields = append(fields, words[0])
		case "sequencer/head", "sequencer/abort-safety", "rebase-merge/onto", "rebase-merge/orig-head", "rebase-merge/stopped-sha", "rebase-apply/original-commit":
			fields = append(fields, words[0])
		case "rebase-merge/rewritten-list":
			fields = append(fields, words...)
		}
	}
	return fields
}

func restoreArchivedGitMetadata(ctx context.Context, recovery, destination string) error {
	root, err := os.OpenRoot(recovery)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), "metadata", func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) && path == "metadata" {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return errors.New("unsafe archived git metadata")
		}
		if entry.IsDir() {
			return nil
		}
		data, err := root.ReadFile(filepath.FromSlash(path))
		if err != nil {
			return err
		}
		relative := strings.TrimPrefix(path, "metadata/")
		allowed := false
		for _, name := range archivedGitMetadataPaths {
			if relative == name || strings.HasPrefix(relative, name+"/") {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("unsupported archived git metadata path")
		}
		target := filepath.Join(destination, ".git", filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
}
