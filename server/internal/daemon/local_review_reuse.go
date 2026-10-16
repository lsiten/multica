package daemon

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// reviewDirectoryTask preserves the directory owner's review/cache identity for
// continuations. Local paths alone are never evidence that two tasks may share it.
func (d *Daemon) reviewDirectoryTask(ctx context.Context, request worktreeReviewRequest) (string, error) {
	if _, _, err := d.resolveReviewRoot(ctx, request); err == nil {
		return request.TaskID, nil
	}
	if request.RuntimeID == "" {
		return request.TaskID, nil
	}
	root, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		return request.TaskID, nil
	}
	selected, err := filepath.EvalSymlinks(request.Path)
	if err != nil {
		return request.TaskID, nil
	}
	rel, err := filepath.Rel(root, selected)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return request.TaskID, nil
	}
	for parent := selected; parent != root; parent = filepath.Dir(parent) {
		parentRel, err := filepath.Rel(root, parent)
		if err != nil {
			return "", err
		}
		owner, err := d.gcTaskDirOwner(filepath.Join(d.cfg.WorkspacesRoot, parentRel))
		if err != nil {
			continue
		}
		if owner.WorkspaceID != request.WorkspaceID {
			return "", errors.New("review directory belongs to another workspace")
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		query := url.Values{"directory_task_id": {owner.TaskID}, "path": {request.Path}}
		endpoint := "/api/daemon/runtimes/" + url.PathEscape(request.RuntimeID) + "/tasks/" + url.PathEscape(request.TaskID) + "/review-binding?" + query.Encode()
		var binding protocol.LocalReviewRuntimeBinding
		if err := d.client.getJSON(ctx, endpoint, &binding); err != nil {
			return "", errors.New("reused review directory verification unavailable")
		}
		if binding.TaskID != owner.TaskID || binding.WorkspaceID != request.WorkspaceID || binding.RuntimeID != request.RuntimeID {
			return "", errors.New("reused review directory identity mismatch")
		}
		return owner.TaskID, nil
	}
	return request.TaskID, nil
}

// bindVerifiedExternalReviewDirectory repairs the provenance marker for a
// local project directory created by an older daemon. The server verifies the
// task's persisted work_dir through the directory_task_id/path query before we
// persist a durable binding, so a client cannot turn an arbitrary local Git
// repository into a review checkout.
func (d *Daemon) bindVerifiedExternalReviewDirectory(ctx context.Context, request worktreeReviewRequest) (string, string, error) {
	if d.client == nil || request.RuntimeID == "" || request.TaskID == "" || request.WorkspaceID == "" {
		return "", "", errors.New("external review directory proof unavailable")
	}
	canonical, err := filepath.EvalSymlinks(request.Path)
	if err != nil {
		return "", "", err
	}
	// Git is optional for the file browser. Keep the canonical directory as
	// the review path when it is not a repository; Git-specific actions will
	// report their own capability/error, while read-only workspace_files and
	// file_content can still inspect the verified local directory.
	reviewPath := canonical
	if gitRoot, gitErr := worktreeGit(ctx, canonical, "rev-parse", "--show-toplevel"); gitErr == nil {
		reviewPath = gitRoot
	}
	query := url.Values{
		"directory_task_id": {request.TaskID},
		"path":              {request.Path},
	}
	endpoint := "/api/daemon/runtimes/" + url.PathEscape(request.RuntimeID) + "/tasks/" + url.PathEscape(request.TaskID) + "/review-binding?" + query.Encode()
	var binding protocol.LocalReviewRuntimeBinding
	if err := d.client.getJSON(ctx, endpoint, &binding); err != nil || binding.WorkspaceID != request.WorkspaceID || binding.TaskID != request.TaskID || binding.RuntimeID != request.RuntimeID {
		return "", "", errors.New("external review directory identity mismatch")
	}
	archive := execenv.ReviewArchivePath(d.cfg.WorkspacesRoot, request.WorkspaceID, request.TaskID)
	if err := os.MkdirAll(archive, 0o700); err != nil {
		return "", "", err
	}
	if err := execenv.WriteReviewDirectory(archive, execenv.ReviewDirectory{
		WorkspaceID: request.WorkspaceID,
		TaskID:      request.TaskID,
		Path:        reviewPath,
		SourcePath:  canonical,
	}); err != nil {
		return "", "", err
	}
	if err := execenv.WriteReviewRuntime(archive, execenv.ReviewRuntime{
		WorkspaceID: request.WorkspaceID,
		TaskID:      request.TaskID,
		RuntimeID:   request.RuntimeID,
		AgentID:     binding.AgentID,
	}); err != nil {
		return "", "", err
	}
	return reviewPath, archive, nil
}
