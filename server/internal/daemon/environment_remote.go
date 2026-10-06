package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"net/http"
	"path/filepath"
)

func (d *Daemon) executeEnvironmentCommand(ctx context.Context, scope environmentOperationScope, command protocol.EnvironmentCommand) (any, error) {
	if err := command.Validate(); err != nil {
		return nil, err
	}
	if !d.environmentRuntimeOwnedHere(scope) {
		return nil, errors.New("runtime workspace ownership changed")
	}
	switch command.Action {
	case "policy":
		return d.environmentPolicyStatus(scope)
	case "policy_update":
		if err := d.saveEnvironmentPolicy(scope, *command.Policy); err != nil {
			return nil, err
		}
		return d.environmentPolicyStatus(scope)
	case "operations":
		return d.listEnvironmentOperations(scope)
	case "operation_start":
		return d.startEnvironmentOperation(scope, *command.Operation)
	case "operation_status", "operation_cancel":
		return d.environmentOperationStatus(scope, command.OperationID, command.Action == "operation_cancel")
	case "archives":
		rows, err := d.listEnvironmentArchives(ctx, scope.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if scope.RuntimeID == "" {
			return rows, nil
		}
		filtered := []worktreeArchiveSummary{}
		for _, row := range rows {
			manifest, err := execenv.ReadEnvironmentArchive(d.cfg.WorkspacesRoot, row.ArchiveID)
			if err != nil {
				continue
			}
			path := filepath.Join(d.cfg.WorkspacesRoot, filepath.FromSlash(manifest.RelativeRoot))
			status, err := d.environmentTaskGCStatus(ctx, path, &manifest.Owner, manifest.Metadata)
			if err != nil || status.WorkspaceID != scope.WorkspaceID || status.RuntimeID != scope.RuntimeID {
				continue
			}
			row.RuntimeID = status.RuntimeID
			filtered = append(filtered, row)
		}
		return filtered, nil
	}
	roots, err := d.environmentRootPaths(ctx)
	if err != nil {
		return nil, err
	}
	ctx = d.prefetchEnvironmentLifecycles(ctx, roots)
	ctx = d.withEnvironmentReviewReferences(ctx, roots)
	paths, err := d.authorizedEnvironmentPaths(ctx, scope, roots)
	if err != nil {
		return nil, err
	}
	ctx = withEnvironmentScope(ctx, scope)
	switch command.Action {
	case "inventory":
		allowed := make(map[string]bool, len(paths))
		for _, path := range paths {
			allowed[path] = true
		}
		rows, err := d.managedWorktreesForPaths(ctx, allowed)
		if err != nil {
			return nil, err
		}
		for index := range rows {
			if scope.RuntimeID != "" {
				rows[index].RuntimeID = scope.RuntimeID
			}
		}
		return rows, nil
	case "cache_preview":
		results := []worktreeCacheResult{}
		for id, path := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			result := d.worktreeCacheOperation(ctx, path, "")
			result.EnvironmentID = id
			results = append(results, result)
		}
		return results, nil
	case "cleanup_preview":
		results := []environmentCleanupResult{}
		for id, path := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			result := d.cleanupUnreferencedEnvironment(ctx, path, "")
			result.EnvironmentID = id
			results = append(results, result)
		}
		return results, nil
	case "archive_preview":
		results := []worktreeArchiveResult{}
		for id, path := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			result := d.archiveEnvironmentOperation(ctx, path, "", "")
			result.EnvironmentID = id
			results = append(results, result)
		}
		return results, nil
	default:
		return nil, errors.New("unknown environment command")
	}
}

func (d *Daemon) runRemoteEnvironment(ctx context.Context, command protocol.LocalReviewCommand) protocol.LocalReviewResult {
	result := protocol.LocalReviewResult{ClaimToken: command.ClaimToken}
	if command.Action != "environment" || command.Environment == nil || command.WorkspaceID == "" || command.RuntimeID == "" || command.ActorID == "" || command.Path != "" || command.TaskID != "" {
		result.Error = "invalid scoped environment command"
		return result
	}
	value, err := d.executeEnvironmentCommand(ctx, environmentOperationScope{WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID}, *command.Environment)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Page, err = json.Marshal(value)
	if err != nil {
		result.Error = "environment response encoding failed"
	}
	return result
}

func (d *Daemon) environmentOperationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		protocol.EnvironmentCommand
		WorkspaceID string `json:"workspace_id"`
		RuntimeID   string `json:"runtime_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.EnvironmentCommand.Validate() != nil {
		http.Error(w, "invalid environment command", http.StatusBadRequest)
		return
	}
	value, err := d.executeEnvironmentCommand(r.Context(), environmentOperationScope{WorkspaceID: request.WorkspaceID, RuntimeID: request.RuntimeID}, request.EnvironmentCommand)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		d.logger.Debug("environment command response interrupted", "error", err)
	}
}
