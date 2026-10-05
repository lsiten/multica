package execenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type sharedDirectoryGuard struct {
	Path     string `json:"path"`
	Previous []byte `json:"previous,omitempty"`
	Existed  bool   `json:"existed"`
	Mode     uint32 `json:"mode"`
	Written  []byte `json:"written"`
}

// SharedDirectoryLease pins shared metadata across daemon processes. Each
// participant owns a kernel lock so a crashed process leaves no live lease.
type SharedDirectoryLease struct {
	path     string
	stateDir string
	name     string
	file     *os.File
	mu       sync.Mutex
	finished bool
	run      SharedWorktreeDelivery
}

func sharedDirectoryStateDir(path string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(path))
	return filepath.Join(cache, "multica", "shared-directories", hex.EncodeToString(hash[:])), nil
}

// SharedDirectoryUnsettled keeps live or not-yet-delivered work out of GC.
func SharedDirectoryUnsettled(ctx context.Context, path string) (bool, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	dir, err := sharedDirectoryStateDir(canonical)
	if err != nil {
		return true, err
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return true, err
	}
	unlock, err := lockSharedDirectoryState(ctx, dir)
	if err != nil {
		return true, err
	}
	defer unlock()
	live, err := liveSharedDirectoryUsers(dir, "")
	if err != nil || live > 0 {
		return true, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "delivery-") {
			return true, nil
		}
	}
	return false, nil
}

func lockSharedDirectoryState(ctx context.Context, dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := openLockFile(filepath.Join(dir, "metadata.lock"))
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		locked, err := lockFileExclusiveNonBlocking(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		if locked {
			return func() { releaseLockFile(file) }, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func liveSharedDirectoryUsers(dir, exclude string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	live := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "participant-") || entry.Name() == exclude {
			continue
		}
		if !entry.Type().IsRegular() {
			return 0, errors.New("invalid shared directory participant")
		}
		path := filepath.Join(dir, entry.Name())
		file, err := openLockFile(path)
		if err != nil {
			return 0, err
		}
		locked, err := lockFileExclusiveNonBlocking(file)
		if err != nil {
			file.Close()
			return 0, err
		}
		if !locked {
			live++
			file.Close()
			continue
		}
		if data, readErr := os.ReadFile(path); readErr != nil {
			releaseLockFile(file)
			return 0, readErr
		} else if len(data) > 0 {
			var receipt SharedWorktreeDelivery
			if err := json.Unmarshal(data, &receipt); err != nil {
				releaseLockFile(file)
				return 0, err
			}
			if err := preserveSharedWorktreeReceipt(dir, receipt); err != nil {
				releaseLockFile(file)
				return 0, err
			}
		}
		releaseLockFile(file)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}
	return live, nil
}

func restoreSharedDirectoryGuard(dir string) error {
	file := filepath.Join(dir, "guard.json")
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var guard sharedDirectoryGuard
	if err := json.Unmarshal(data, &guard); err != nil {
		return err
	}
	marker := filepath.Join(guard.Path, TaskContextMarkerRelPath)
	if info, err := os.Lstat(filepath.Dir(marker)); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("shared task marker directory changed")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, err := os.Lstat(marker); err == nil && !info.Mode().IsRegular() {
		return errors.New("shared task marker changed to a non-regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current, err := os.ReadFile(marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if bytes.Equal(current, guard.Written) {
		if guard.Existed {
			if err := writeFileAtomic(marker, guard.Previous, os.FileMode(guard.Mode)); err != nil {
				return err
			}
		} else {
			if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			// Remove only an empty directory; user-created files remain intact.
			_ = os.Remove(filepath.Dir(marker))
		}
	}
	return os.Remove(file)
}

// UseSharedDirectory installs a task-neutral CLI guard and joins its shared
// lifecycle. Setup and final settlement are serialized; file edits are not.
func UseSharedDirectory(ctx context.Context, path string) (*SharedDirectoryLease, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(filepath.Join(canonical, ".multica")); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("shared task marker directory is not a regular directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	dir, err := sharedDirectoryStateDir(canonical)
	if err != nil {
		return nil, err
	}
	unlock, err := lockSharedDirectoryState(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	live, err := liveSharedDirectoryUsers(dir, "")
	if err != nil {
		return nil, err
	}
	if live == 0 {
		if err := restoreSharedDirectoryGuard(dir); err != nil {
			return nil, err
		}
		marker := filepath.Join(canonical, TaskContextMarkerRelPath)
		previous, err := os.ReadFile(marker)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		guard := sharedDirectoryGuard{Path: canonical, Previous: previous, Existed: err == nil, Mode: 0644}
		if guard.Existed {
			info, err := os.Lstat(marker)
			if err != nil || !info.Mode().IsRegular() {
				return nil, errors.New("invalid shared task guard")
			}
			var previousMarker taskContextMarkerFile
			if json.Unmarshal(previous, &previousMarker) != nil || previousMarker.ManagedBy != TaskContextMarkerManagedBy {
				return nil, errors.New("shared task guard is not daemon-owned")
			}
			guard.Mode = uint32(info.Mode().Perm())
		}
		guard.Written, err = json.MarshalIndent(taskContextMarkerFile{ManagedBy: TaskContextMarkerManagedBy}, "", "  ")
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(guard)
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(filepath.Join(dir, "guard.json"), data, 0600); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(marker, guard.Written, 0644); err != nil {
			return nil, err
		}
	}
	name := "participant-" + rand.Text()
	file, err := openLockFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	locked, err := lockFileExclusiveNonBlocking(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("claim shared participant: %w", err)
	}
	if !locked {
		file.Close()
		return nil, errors.New("shared participant already claimed")
	}
	return &SharedDirectoryLease{path: canonical, stateDir: dir, name: name, file: file}, nil
}

// Finish executes settlement while admissions are paused. last is true only
// after every other process has released its participant. It is idempotent.
func (l *SharedDirectoryLease) Finish(ctx context.Context, settle func(last bool) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return nil
	}
	// Failed settlement leaves the participant record replayable but must not
	// keep a kernel lock held after its execution has ended.
	defer func() {
		if !l.finished {
			releaseLockFile(l.file)
			l.finished = true
		}
	}()
	unlock, err := lockSharedDirectoryState(ctx, l.stateDir)
	if err != nil {
		return err
	}
	defer unlock()
	live, err := liveSharedDirectoryUsers(l.stateDir, l.name)
	if err != nil {
		return err
	}
	last := live == 0
	if err := preserveSharedWorktreeReceipt(l.stateDir, l.run); err != nil {
		return err
	}
	if last {
		if err := restoreSharedDirectoryGuard(l.stateDir); err != nil {
			return err
		}
	}
	if settle != nil {
		err = settle(last)
	}
	releaseLockFile(l.file)
	l.finished = true
	removeErr := os.Remove(filepath.Join(l.stateDir, l.name))
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(err, removeErr)
}
