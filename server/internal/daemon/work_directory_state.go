package daemon

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (c *Client) listRuntimeWorkDirectories(ctx context.Context, runtimeID, after string) ([]string, error) {
	var page protocol.RuntimeWorkDirectoryPage
	endpoint := "/api/daemon/runtimes/" + url.PathEscape(runtimeID) + "/work-directories?after=" + url.QueryEscape(after)
	if err := c.getJSON(ctx, endpoint, &page); err != nil {
		return nil, err
	}
	if len(page.Paths) > 500 {
		return nil, errors.New("work directory page exceeds limit")
	}
	for _, path := range page.Paths {
		if !filepath.IsAbs(path) || path <= after {
			return nil, errors.New("invalid work directory page")
		}
		after = path
	}
	return page.Paths, nil
}

func (d *Daemon) reportWorkDirectoryStates(ctx context.Context, runtimeID string, paths []string) error {
	states := make([]protocol.WorkDirectoryState, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			states = append(states, protocol.WorkDirectoryState{Path: path, Missing: true})
		case err == nil:
			states = append(states, protocol.WorkDirectoryState{Path: path, Missing: !info.IsDir()})
		default:
			// Permission and I/O errors do not prove a directory was removed.
			d.logger.Debug("work directory state unavailable", "runtime_id", runtimeID, "path", path, "error", err)
		}
	}
	if len(states) == 0 {
		return nil
	}
	endpoint := "/api/daemon/runtimes/" + url.PathEscape(runtimeID) + "/work-directories"
	return d.client.postJSON(ctx, endpoint, states, nil)
}

func (d *Daemon) reportTaskWorkDirectoryState(ctx context.Context, status *protocol.TaskGCStatus) {
	if d.client == nil || status == nil || status.RuntimeID == "" || !filepath.IsAbs(status.WorkDir) {
		return
	}
	// The filesystem change has already happened, even if its operation ended.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := d.reportWorkDirectoryStates(ctx, status.RuntimeID, []string{status.WorkDir}); err != nil {
		d.logger.Warn("work directory state report failed; periodic reconciliation will retry", "runtime_id", status.RuntimeID, "error", err)
	}
}

func (d *Daemon) reconcileRuntimeWorkDirectories(ctx context.Context, runtimeID, after string) (string, error) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		paths, err := d.client.listRuntimeWorkDirectories(readCtx, runtimeID, after)
		cancel()
		if err != nil {
			return after, err
		}
		reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = d.reportWorkDirectoryStates(reportCtx, runtimeID, paths)
		cancel()
		if err != nil {
			return after, err
		}
		if len(paths) < 500 {
			return "", nil
		}
		after = paths[len(paths)-1]
	}
}

// Reconcile history even when GC is disabled: old versions and manual removals
// may have deleted directories without updating their server records.
func (d *Daemon) workDirectoryStateLoop(ctx context.Context) {
	cursors := make(map[string]string)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		for _, runtimeID := range d.allRuntimeIDs() {
			if ctx.Err() != nil {
				return
			}
			scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			after, err := d.reconcileRuntimeWorkDirectories(scanCtx, runtimeID, cursors[runtimeID])
			cancel()
			cursors[runtimeID] = after
			if err != nil {
				d.logger.Debug("work directory reconciliation deferred", "runtime_id", runtimeID, "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
