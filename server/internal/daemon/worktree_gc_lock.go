package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func (d *Daemon) lockGCTaskDirectory(path string) (func(), error) {
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(d.cfg.WorkspacesRoot, path)
	if err != nil || !filepath.IsLocal(relative) {
		root.Close()
		return nil, errors.New("invalid task directory")
	}
	claim, info, err := execenv.LockEnvRootForReuse(root, relative, path)
	if err != nil || claim == nil {
		root.Close()
		return nil, errors.New("task directory is busy or unavailable")
	}
	current, err := root.Lstat(relative)
	if err != nil || !os.SameFile(info, current) || current.Mode()&linkedDirModes != 0 {
		claim.Release()
		root.Close()
		return nil, errors.New("task directory identity changed")
	}
	if reserved, err := execenv.PhysicalRootReserved(path); err != nil || reserved {
		claim.Release()
		root.Close()
		return nil, errors.New("physical root handoff remains pending")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	releaseUsers, available, err := execenv.ReservePhysicalRootMutation(ctx, path)
	cancel()
	if err != nil || !available {
		claim.Release()
		root.Close()
		return nil, errors.New("physical environment has live or undelivered consumers")
	}
	return func() { releaseUsers(); claim.Release(); root.Close() }, nil
}
