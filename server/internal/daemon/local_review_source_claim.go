package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// claimReviewSource protects a reused managed checkout whose task root differs
// from the current review's record root. The caller already holds the latter.
func (d *Daemon) claimReviewSource(ctx context.Context, path, recordRoot string) (func(), error) {
	workspacePath, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		return nil, err
	}
	if relative, err := filepath.Rel(workspacePath, path); err != nil || !filepath.IsLocal(relative) {
		return func() {}, nil // External local directories use LocalPathLocker.
	}
	recordPath, err := filepath.EvalSymlinks(recordRoot)
	if err != nil {
		return nil, err
	}
	for parent := path; parent != workspacePath; parent = filepath.Dir(parent) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(workspacePath, parent)
		if err != nil {
			return nil, err
		}
		logical := filepath.Join(d.cfg.WorkspacesRoot, relative)
		if _, err := d.gcTaskDirOwner(logical); err != nil {
			continue
		}
		if parent == recordPath {
			return func() {}, nil
		}
		release, available := d.reserveEnvRootForGC(logical)
		if !available {
			return nil, errors.New("reused source task environment is busy")
		}
		workspace, err := os.OpenRoot(d.cfg.WorkspacesRoot)
		if err != nil {
			release()
			return nil, err
		}
		claim, _, err := execenv.LockEnvRootForReuse(workspace, relative, logical)
		if err != nil || claim == nil {
			workspace.Close()
			release()
			return nil, errors.New("reused source task environment is locked")
		}
		return func() { claim.Release(); workspace.Close(); release() }, nil
	}
	return nil, errors.New("managed review source has no task owner")
}
