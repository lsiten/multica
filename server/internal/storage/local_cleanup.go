package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

// LocalUploadObject groups an object with its sidecar and interrupted write.
// The private file snapshots prevent cleanup from deleting a changed upload.
type LocalUploadObject struct {
	Key    string
	Bytes  int64
	Recent bool
	files  map[string]fs.FileInfo
}

func (s *LocalStorage) CleanupInventory(ctx context.Context, cutoff time.Time) ([]LocalUploadObject, error) {
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		return nil, fmt.Errorf("open upload root: %w", err)
	}
	defer root.Close()
	objects := make(map[string]*LocalUploadObject)
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		key := cleanupObjectKey(name)
		object := objects[key]
		if object == nil {
			object = &LocalUploadObject{Key: key, files: make(map[string]fs.FileInfo)}
			objects[key] = object
		}
		object.files[name] = info
		object.Bytes += info.Size()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan upload root: %w", err)
	}
	var result []LocalUploadObject
	for _, object := range objects {
		for _, info := range object.files {
			if !info.ModTime().Before(cutoff) {
				object.Recent = true
			}
		}
		result = append(result, *object)
	}
	return result, nil
}

func cleanupObjectKey(name string) string {
	if strings.HasSuffix(name, metaSuffix) {
		return strings.TrimSuffix(name, metaSuffix)
	}
	base := path.Base(name)
	if strings.HasPrefix(base, ".") && strings.HasSuffix(base, tempSuffix) {
		return path.Join(path.Dir(name), strings.TrimSuffix(strings.TrimPrefix(base, "."), tempSuffix))
	}
	return name
}

// DeleteCleanupObject refuses changed groups and never follows an out-of-root
// symlink. Missing files are success, so failed deletions can be retried.
func (s *LocalStorage) DeleteCleanupObject(ctx context.Context, object LocalUploadObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		return fmt.Errorf("open upload root: %w", err)
	}
	defer root.Close()
	paths := []string{object.Key, object.Key + metaSuffix, path.Join(path.Dir(object.Key), "."+path.Base(object.Key)+tempSuffix)}
	for _, name := range paths {
		current, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect cleanup object: %w", err)
		}
		previous := object.files[name]
		if previous == nil || !current.Mode().IsRegular() || !os.SameFile(previous, current) ||
			current.Size() != previous.Size() || !current.ModTime().Equal(previous.ModTime()) {
			return fmt.Errorf("upload changed since cleanup scan: %s", object.Key)
		}
	}
	for _, name := range paths {
		if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove cleanup object: %w", err)
		}
	}
	return nil
}
