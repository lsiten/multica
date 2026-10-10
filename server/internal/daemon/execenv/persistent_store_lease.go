package execenv

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/util"
)

func persistentStoreState(store string) (string, error) {
	if !filepath.IsAbs(store) {
		return "", errors.New("persistent store path must be absolute")
	}
	canonical, err := util.ResolveSymlinksBestEffort(store)
	if err != nil {
		return "", err
	}
	// Separate from checkout participants: provider stores must not acquire task
	// context markers or participate in Git delivery.
	state, err := sharedDirectoryStateDir(canonical)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Dir(state)), "persistent-stores", filepath.Base(state)), nil
}

// UsePersistentStore gives the task owner a kernel-held participant before its
// private helper mounts a session/memory store. No provider files are read.
func UsePersistentStore(ctx context.Context, store string) (*SharedDirectoryLease, error) {
	dir, err := persistentStoreState(store)
	if err != nil {
		return nil, err
	}
	unlock, err := lockSharedDirectoryState(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	name := "participant-" + rand.Text()
	file, err := openLockFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	locked, err := lockFileExclusiveNonBlocking(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	if !locked {
		file.Close()
		return nil, errors.New("persistent store participant collision")
	}
	return &SharedDirectoryLease{path: store, stateDir: dir, name: name, file: file}, nil
}

// ReservePersistentStoreDeletion excludes new participants through deletion.
// An unavailable state or live kernel lease denies removal; elapsed time never
// substitutes for process ownership proof.
func ReservePersistentStoreDeletion(ctx context.Context, store string) (func(), bool, error) {
	dir, err := persistentStoreState(store)
	if err != nil {
		return nil, false, err
	}
	unlock, err := lockSharedDirectoryState(ctx, dir)
	if err != nil {
		return nil, false, err
	}
	live, err := liveSharedDirectoryUsers(dir, "")
	if err != nil || live > 0 {
		unlock()
		return nil, false, err
	}
	return unlock, true, nil
}
