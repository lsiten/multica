package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Resource commands enter only through the authenticated server relay. They do
// not acquire task provenance or participate in task directory garbage collection.
func (d *Daemon) runProjectDirectoryRead(ctx context.Context, command protocol.LocalReviewCommand) protocol.LocalReviewResult {
	result := protocol.LocalReviewResult{ClaimToken: command.ClaimToken}
	if command.TaskID != "" || command.WorkspaceID == "" || !filepath.IsAbs(command.Path) || !protocol.IsProjectDirectoryRead(command.Action) {
		result.Error = "invalid read-only project directory request"
		return result
	}
	if err := localReviewOperations.Lock(ctx); err != nil {
		result.Error = err.Error()
		return result
	}
	defer localReviewOperations.Unlock()
	path, err := filepath.EvalSymlinks(command.Path)
	if err != nil {
		result.Error = "project directory unavailable"
		return result
	}
	// Git commands can implicitly walk to a parent repository. Require an exact
	// checkout root before exposing index data for a bound resource directory.
	if command.Action == "index" {
		if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			result.Error = "project directory is not a Git checkout root"
			return result
		}
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(command.WorkspaceID+"\x00"+command.RuntimeID+"\x00"+command.ProjectResourceID+"\x00"+path)))
	root := filepath.Join(d.cfg.WorkspacesRoot, ".project-directory-reviews", key)
	if command.Action == "index" {
		if err := os.MkdirAll(root, 0700); err != nil {
			result.Error = "cannot create project directory cache"
			return result
		}
	}
	request := worktreeReviewRequest{Action: command.Action, Path: command.Path, Target: command.Target, WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, RelativePath: command.RelativePath, Recursive: command.Recursive, VersionID: command.VersionID, Offset: command.Offset, Limit: command.Limit}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/project-directory/read", nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	capture := &localReviewCapture{header: http.Header{}, status: http.StatusOK}
	d.pagedReviewRead(capture, req, pagedReviewContext{request: request, path: path, root: root})
	if capture.status != http.StatusOK {
		result.Error = capture.body.String()
		return result
	}
	result.Page = append(result.Page, capture.body.Bytes()...)
	return result
}
