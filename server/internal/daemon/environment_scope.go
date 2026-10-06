package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentScopeContextKey struct{}

func withEnvironmentScope(ctx context.Context, scope environmentOperationScope) context.Context {
	return context.WithValue(ctx, environmentScopeContextKey{}, scope)
}

func validScopedTaskStatus(ctx context.Context, owner *execenv.EnvRootOwner, status *protocol.TaskGCStatus) bool {
	if status == nil {
		return false
	}
	scope, scoped := ctx.Value(environmentScopeContextKey{}).(environmentOperationScope)
	if !scoped {
		return true
	}
	if scope.WorkspaceID != "" && owner.WorkspaceID != scope.WorkspaceID {
		return false
	}
	if status.WorkspaceID != "" && status.WorkspaceID != owner.WorkspaceID {
		return false
	}
	if scope.RuntimeID != "" {
		return status.WorkspaceID == scope.WorkspaceID && status.RuntimeID == scope.RuntimeID
	}
	return true
}

func (d *Daemon) environmentRuntimeOwnedHere(scope environmentOperationScope) bool {
	if scope.RuntimeID == "" {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	workspace := d.workspaces[scope.WorkspaceID]
	if workspace == nil {
		return false
	}
	// Automatic recovery is authorized by fresh, workspace-scoped task facts
	// even after the runtime has been retired. Interactive commands still need
	// a currently registered runtime owned by this daemon.
	if scope.Automatic {
		return true
	}
	if _, exists := d.runtimeIndex[scope.RuntimeID]; !exists {
		return false
	}
	for _, runtime := range workspace.runtimeIDs {
		if runtime == scope.RuntimeID {
			return true
		}
	}
	return false
}

func (d *Daemon) scopedEnvironmentPaths(ctx context.Context, scope environmentOperationScope) (map[string]string, error) {
	if !d.environmentRuntimeOwnedHere(scope) {
		return nil, errors.New("runtime is no longer owned by this workspace and daemon")
	}
	roots, err := d.environmentRootPaths(ctx)
	if err != nil {
		return nil, err
	}
	ctx = d.prefetchEnvironmentLifecycles(ctx, roots)
	return d.authorizedEnvironmentPaths(ctx, scope, roots)
}

func (d *Daemon) environmentRootPaths(ctx context.Context) ([]string, error) {
	workspaces, err := os.ReadDir(d.cfg.WorkspacesRoot)
	if err != nil {
		return nil, err
	}
	roots := []string{}
	for _, workspace := range workspaces {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !workspace.IsDir() || strings.HasPrefix(workspace.Name(), ".") {
			continue
		}
		parent := filepath.Join(d.cfg.WorkspacesRoot, workspace.Name())
		children, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, child := range children {
			if child.IsDir() && !strings.HasPrefix(child.Name(), ".") {
				roots = append(roots, filepath.Join(parent, child.Name()))
			}
		}
	}
	return roots, nil
}

func (d *Daemon) authorizedEnvironmentPaths(ctx context.Context, scope environmentOperationScope, roots []string) (map[string]string, error) {
	paths := make(map[string]string)
	var mu sync.Mutex
	queue := make(chan string, len(roots))
	for _, path := range roots {
		queue <- path
	}
	close(queue)
	var workers sync.WaitGroup
	for range min(8, len(roots)) {
		workers.Go(func() {
			for path := range queue {
				if ctx.Err() != nil {
					return
				}
				owner, err := d.gcTaskDirOwner(path)
				if err != nil || (scope.WorkspaceID != "" && owner.WorkspaceID != scope.WorkspaceID) {
					continue
				}
				if scope.RuntimeID != "" {
					if d.client == nil {
						continue
					}
					status, err := d.environmentTaskGCStatus(ctx, path, owner, nil)
					if err != nil || !validScopedTaskStatus(withEnvironmentScope(ctx, scope), owner, status) {
						continue
					}
				}
				id := d.managedEnvironmentID(path, owner.WorkspaceID, owner.TaskID)
				mu.Lock()
				paths[id] = path
				mu.Unlock()
			}
		})
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (d *Daemon) runScopedEnvironmentMutation(ctx context.Context, scope environmentOperationScope, request protocol.EnvironmentOperationRequest, selection protocol.EnvironmentSelection, path string) (any, error) {
	if !d.environmentRuntimeOwnedHere(scope) {
		return nil, errors.New("runtime scope changed")
	}
	ctx = withEnvironmentScope(ctx, scope)
	switch request.Action {
	case "clean_cache":
		result := d.worktreeCacheOperation(ctx, path, selection.Revision)
		result.EnvironmentID = selection.EnvironmentID
		result.Candidates = []worktreeCacheCandidate{}
		return result, nil
	case "archive":
		result := d.archiveEnvironmentOperation(ctx, path, selection.Revision, request.ID)
		result.EnvironmentID = selection.EnvironmentID
		return result, nil
	case "cleanup", "discard":
		if scope.Automatic || request.Action == "cleanup" && protocol.ValidEnvironmentIdentity(selection.Revision) {
			result := d.cleanupUnreferencedEnvironment(ctx, path, selection.Revision)
			result.EnvironmentID = selection.EnvironmentID
			return result, nil
		}
		reason := d.cleanupManagedWorktree(ctx, worktreeCleanup{path: path, discardChanges: request.Action == "discard"})
		return map[string]any{"environment_id": selection.EnvironmentID, "reason": reason, "reclaimed": reason == ""}, nil
	default:
		return nil, errors.New("unknown environment mutation")
	}
}

func (d *Daemon) runScopedEnvironmentRestore(ctx context.Context, scope environmentOperationScope, archiveID string) (worktreeArchiveResult, error) {
	if !d.environmentRuntimeOwnedHere(scope) {
		return worktreeArchiveResult{}, errors.New("runtime scope changed")
	}
	return d.restoreEnvironmentOperation(withEnvironmentScope(ctx, scope), archiveID, scope.WorkspaceID), nil
}
