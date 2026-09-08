package daemon

import (
	"context"
	"errors"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"net/url"
	"os"
	"time"
)

func (d *Daemon) legacyReviewRuntime(ctx context.Context, request worktreeReviewRequest) (execenv.ReviewRuntime, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var binding protocol.LocalReviewRuntimeBinding
	path := "/api/daemon/runtimes/" + url.PathEscape(request.RuntimeID) + "/tasks/" + url.PathEscape(request.TaskID) + "/review-binding"
	if err := d.client.getJSON(ctx, path, &binding); err != nil {
		return execenv.ReviewRuntime{}, errors.New("legacy worktree runtime verification unavailable")
	}
	if binding.RuntimeID != request.RuntimeID || binding.TaskID != request.TaskID || binding.WorkspaceID != request.WorkspaceID {
		return execenv.ReviewRuntime{}, errors.New("legacy worktree runtime identity mismatch")
	}
	return execenv.ReviewRuntime{WorkspaceID: binding.WorkspaceID, TaskID: binding.TaskID, RuntimeID: binding.RuntimeID, AgentID: binding.AgentID}, nil
}

// The caller holds the task environment's reservation and cross-process lock.
func persistLegacyReviewRuntime(root string, binding execenv.ReviewRuntime) error {
	existing, err := execenv.ReadReviewRuntime(root)
	if err == nil {
		if existing.WorkspaceID != binding.WorkspaceID || existing.TaskID != binding.TaskID || existing.RuntimeID != binding.RuntimeID {
			return errors.New("runtime binding changed during legacy verification")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return execenv.WriteReviewRuntime(root, binding)
}
