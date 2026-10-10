package execenv

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
)

// ReserveSharedDirectoryPaths excludes new code participants while review
// mutates any overlapping path. Existing participants or unresolved settlement
// make the review unavailable; task/session age is never ownership evidence.
func ReserveSharedDirectoryPaths(ctx context.Context, paths []string) (func(), bool, error) {
	return reserveSharedDirectoryPaths(ctx, paths, false)
}

func reserveSharedDirectoryPaths(ctx context.Context, paths []string, protectDeliveries bool) (func(), bool, error) {
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		resolved, err := util.ResolveSymlinksBestEffort(path)
		if err != nil {
			return nil, false, err
		}
		canonical = append(canonical, resolved)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, false, err
	}
	root := filepath.Join(cache, "multica", "shared-directories")
	var unlock func()
	if protectDeliveries {
		unlock, err = lockSharedDirectoryState(ctx, filepath.Join(root, ".admission"))
	} else {
		var acquired bool
		unlock, acquired, err = tryLockSharedDirectoryState(filepath.Join(root, ".admission"))
		if err == nil && !acquired {
			return nil, false, nil
		}
	}
	if err != nil {
		return nil, false, err
	}
	admitted := false
	defer func() {
		if !admitted {
			unlock()
		}
	}()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, false, err
	}
	if len(entries) > 16384 {
		return nil, false, errors.New("shared participant inventory exceeds bound")
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		var release func()
		if protectDeliveries {
			release, err = lockSharedDirectoryState(ctx, dir)
		} else {
			var acquired bool
			release, acquired, err = tryLockSharedDirectoryState(dir)
			if err == nil && !acquired {
				return nil, false, nil
			}
		}
		if err != nil {
			return nil, false, err
		}
		path, busy, err := sharedDirectoryReviewState(dir, protectDeliveries)
		release()
		if err != nil {
			return nil, false, err
		}
		if !busy {
			continue
		}
		for _, candidate := range canonical {
			forward, e1 := filepath.Rel(path, candidate)
			reverse, e2 := filepath.Rel(candidate, path)
			if e1 == nil && filepath.IsLocal(forward) || e2 == nil && filepath.IsLocal(reverse) {
				return nil, false, nil
			}
		}
	}
	admitted = true
	return unlock, true, nil
}

func sharedDirectoryReviewState(dir string, protectDeliveries bool) (string, bool, error) {
	pending, err := pendingPhysicalFinish(dir)
	if err != nil {
		return "", false, err
	}
	// Pending physical finish owns a participant which the generic reaper must
	// not consume. Its durable permit retains the exact checkout path.
	if pending {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", false, err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "finish-participant-") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				return "", false, err
			}
			var permit PhysicalFinishPermit
			if err = json.Unmarshal(data, &permit); err != nil {
				return "", false, err
			}
			expected, err := sharedDirectoryStateDir(permit.Participant.Path)
			if err != nil || expected != dir {
				return "", false, errors.New("physical finish directory scope changed")
			}
			return permit.Participant.Path, true, nil
		}
	}
	live, err := liveSharedDirectoryUsers(dir, "")
	if err != nil {
		return "", false, err
	}
	if live == 0 {
		if protectDeliveries {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return "", false, err
			}
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), "delivery-") {
					continue
				}
				receipt, err := readSharedDelivery(filepath.Join(dir, entry.Name()))
				if err != nil {
					return "", false, err
				}
				candidate, err := util.ResolveSymlinksBestEffort(receipt.WorkDir)
				if err != nil {
					return "", false, err
				}
				for {
					expected, err := sharedDirectoryStateDir(candidate)
					if err != nil {
						return "", false, err
					}
					if expected == dir {
						return candidate, true, nil
					}
					parent := filepath.Dir(candidate)
					if parent == candidate {
						break
					}
					candidate = parent
				}
				return "", false, errors.New("pending delivery physical identity unavailable")
			}
		}
		return "", false, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "guard.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, errors.New("live shared directory has no identity guard")
	}
	if err != nil {
		return "", false, err
	}
	var guard sharedDirectoryGuard
	if err = json.Unmarshal(data, &guard); err != nil {
		return "", false, err
	}
	expected, err := sharedDirectoryStateDir(guard.Path)
	if err != nil || expected != dir {
		return "", false, errors.New("shared directory guard scope changed")
	}
	return guard.Path, true, nil
}

// ReservePhysicalRootMutation also protects undelivered work while the caller
// holds the root claim and subsequently acquires its repository locks.
func ReservePhysicalRootMutation(ctx context.Context, path string) (func(), bool, error) {
	return reserveSharedDirectoryPaths(ctx, []string{path}, true)
}

func tryLockSharedDirectoryState(dir string) (func(), bool, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, false, err
	}
	file, err := openLockFile(filepath.Join(dir, "metadata.lock"))
	if err != nil {
		return nil, false, err
	}
	locked, err := lockFileExclusiveNonBlocking(file)
	if err != nil || !locked {
		file.Close()
		return nil, false, err
	}
	return func() { releaseLockFile(file) }, true, nil
}
