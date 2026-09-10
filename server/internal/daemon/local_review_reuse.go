package daemon

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"time"

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
