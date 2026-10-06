package daemon

import (
	"context"
	"errors"
	"os"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentLifecycleSnapshotKey struct{}
type environmentLifecycleSnapshot map[string]*protocol.TaskGCStatus

func (d *Daemon) environmentTaskStatus(ctx context.Context, taskID string) (*protocol.TaskGCStatus, error) {
	if snapshot, ok := ctx.Value(environmentLifecycleSnapshotKey{}).(environmentLifecycleSnapshot); ok && snapshot != nil {
		status := snapshot[taskID]
		if status == nil {
			return nil, errors.New("task lifecycle absent from verified batch")
		}
		return status, nil
	}
	return d.client.GetTaskGCCheck(ctx, taskID)
}

// Release authorization is never served from a scan snapshot: a task may have
// resumed or changed review status while its archive was being captured.
func freshEnvironmentLifecycle(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, environmentLifecycleSnapshotKey{}, environmentLifecycleSnapshot(nil))
	return context.WithValue(ctx, environmentReviewReferencesKey{}, map[string]environmentReviewReference(nil))
}

func (d *Daemon) prefetchEnvironmentLifecycles(ctx context.Context, paths []string) context.Context {
	type group struct {
		workspaceID, runtimeID string
		ids                    map[string]bool
	}
	groups := map[environmentOperationScope]*group{}
	snapshot := environmentLifecycleSnapshot{}
	if d.client == nil {
		return context.WithValue(ctx, environmentLifecycleSnapshotKey{}, snapshot)
	}
	for _, path := range paths {
		if ctx.Err() != nil {
			break
		}
		owner, err := d.gcTaskDirOwner(path)
		if err != nil {
			continue
		}
		meta, _ := execenv.ReadGCMeta(path)
		runtimeID := ""
		if meta != nil {
			runtimeID = meta.RuntimeID
		}
		if runtimeID == "" {
			if binding, err := execenv.ReadReviewRuntime(path); err == nil && binding.TaskID == owner.TaskID && binding.WorkspaceID == owner.WorkspaceID {
				runtimeID = binding.RuntimeID
			}
		}
		if runtimeID == "" || !d.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: owner.WorkspaceID, RuntimeID: runtimeID}) {
			status, err := d.client.GetTaskGCCheck(ctx, owner.TaskID)
			if isAccessNotFound(err) {
				runtimeID = ""
			} else if err != nil || status.WorkspaceID != owner.WorkspaceID {
				continue
			} else {
				runtimeID = status.RuntimeID
			}
		}
		scope := environmentOperationScope{WorkspaceID: owner.WorkspaceID, RuntimeID: runtimeID}
		batch := groups[scope]
		if batch == nil {
			batch = &group{owner.WorkspaceID, runtimeID, map[string]bool{}}
			groups[scope] = batch
		}
		batch.ids[owner.TaskID] = true
		if meta != nil {
			if meta.TaskID != "" {
				batch.ids[meta.TaskID] = true
			}
			if meta.LatestTaskID != "" {
				batch.ids[meta.LatestTaskID] = true
			}
		}
		if binding, err := execenv.ReadWorktreeBinding(path); err == nil {
			for _, consumer := range binding.Consumers {
				batch.ids[consumer.TaskID] = true
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			d.logger.Warn("worktree consumer binding unavailable", "task_id", owner.TaskID, "error", err)
		}
	}
	for _, batch := range groups {
		ids := make([]string, 0, len(batch.ids))
		for id := range batch.ids {
			ids = append(ids, id)
		}
		statuses, err := d.client.GetTaskGCChecks(ctx, batch.workspaceID, batch.runtimeID, ids)
		if err != nil {
			d.logger.Warn("environment lifecycle batch unavailable", "workspace_id", batch.workspaceID, "runtime_id", batch.runtimeID, "count", len(ids), "error", err)
			continue
		}
		for id, status := range statuses {
			snapshot[id] = status
		}
	}
	return context.WithValue(ctx, environmentLifecycleSnapshotKey{}, snapshot)
}
