package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// Old snapshots are no longer a retention destination. Profile and authenticated
// workspace checks prevent removal of another daemon's records.
func (d *Daemon) cleanupObsoleteEnvironmentArchives(ctx context.Context) {
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return
	}
	defer root.Close()
	archives, err := root.OpenRoot(".environment-archive")
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		d.logger.Warn("old environment archives unavailable", "error", err)
		return
	}
	defer archives.Close()
	entries, err := fs.ReadDir(archives.FS(), ".")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !entry.IsDir() || entry.Type()&linkedDirModes != 0 {
			continue
		}
		manifest, err := execenv.ReadEnvironmentArchive(d.cfg.WorkspacesRoot, entry.Name())
		if err != nil || manifest.Profile != d.cfg.Profile || manifest.BackendURL != d.cfg.ServerBaseURL {
			continue
		}
		path := filepath.Join(d.cfg.WorkspacesRoot, filepath.FromSlash(manifest.RelativeRoot))
		status, err := d.environmentTaskGCStatus(ctx, path, &manifest.Owner, manifest.Metadata)
		if err != nil || !status.RetentionSupported || status.WorkspaceID != manifest.Owner.WorkspaceID {
			continue
		}
		release, available := d.reserveEnvRootForGC(path)
		if !available {
			continue
		}
		if err := archives.RemoveAll(entry.Name()); err != nil {
			d.logger.Warn("old environment archive deletion failed", "archive_id", entry.Name(), "error", err)
		} else {
			d.logger.Info("old environment archive deleted", "archive_id", entry.Name())
		}
		release()
	}
}
