package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

type worktreeArchiveSelection struct {
	EnvironmentID string `json:"environment_id"`
	Revision      string `json:"revision"`
}

type worktreeArchiveRequest struct {
	Action      string                     `json:"action"`
	WorkspaceID string                     `json:"workspace_id"`
	OperationID string                     `json:"operation_id"`
	ArchiveID   string                     `json:"archive_id"`
	Selections  []worktreeArchiveSelection `json:"selections"`
}

type worktreeArchiveResult struct {
	EnvironmentID string `json:"environment_id"`
	WorkspaceID   string `json:"workspace_id"`
	TaskID        string `json:"task_id"`
	Revision      string `json:"revision"`
	ArchiveID     string `json:"archive_id"`
	Reason        string `json:"reason"`
	OriginalBytes int64  `json:"original_bytes"`
	ArchiveBytes  int64  `json:"archive_bytes"`
	Reclaimed     bool   `json:"reclaimed"`
	Restored      bool   `json:"restored"`
}

type worktreeArchiveSummary struct {
	ArchiveID     string    `json:"archive_id"`
	WorkspaceID   string    `json:"workspace_id"`
	TaskID        string    `json:"task_id"`
	TaskName      string    `json:"task_name"`
	AgentID       string    `json:"agent_id"`
	AgentName     string    `json:"agent_name"`
	RuntimeID     string    `json:"runtime_id"`
	ProjectID     string    `json:"project_id"`
	ProjectName   string    `json:"project_name"`
	SquadID       string    `json:"squad_id"`
	SquadName     string    `json:"squad_name"`
	Kind          string    `json:"kind"`
	OriginalPath  string    `json:"original_path"`
	CreatedAt     time.Time `json:"created_at"`
	ArchiveBytes  int64     `json:"archive_bytes"`
	LogicalBytes  int64     `json:"logical_bytes"`
	RestoreReason string    `json:"restore_reason"`
}

func (d *Daemon) lockArchiveEnvironment(ctx context.Context, path string) (*execenv.EnvRootOwner, func(), string) {
	release, ok := d.reserveEnvRootForGC(path)
	if !ok {
		return nil, nil, "active"
	}
	owner, err := d.gcTaskDirOwner(path)
	if err != nil {
		release()
		return nil, nil, "unowned"
	}
	unlock, err := d.lockGCTaskDirectory(path)
	if err != nil {
		release()
		return nil, nil, "active"
	}
	done := func() { unlock(); release() }
	if d.client == nil {
		done()
		return nil, nil, "unavailable"
	}
	status, err := d.environmentTaskGCStatus(ctx, path, owner, nil)
	if err != nil {
		done()
		return nil, nil, "unavailable"
	}
	if !validScopedTaskStatus(ctx, owner, status) {
		done()
		return nil, nil, "scope_changed"
	}
	if !isAgentTaskTerminal(status.Status) {
		done()
		return nil, nil, "active"
	}
	if scope, ok := ctx.Value(environmentScopeContextKey{}).(environmentOperationScope); ok && scope.Automatic {
		meta, err := execenv.ReadGCMeta(path)
		if err != nil {
			done()
			return nil, nil, "unavailable"
		}
		if eligible, reason := d.automaticArchiveEligible(ctx, path, meta); !eligible {
			done()
			return nil, nil, reason
		}
	}
	if active, err := localreview.HasActiveReview(ctx, path, time.Now()); err != nil {
		done()
		return nil, nil, "unavailable"
	} else if active {
		done()
		return nil, nil, "review"
	}
	return owner, done, ""
}

func archiveOperationID(profile, workspaceID, environmentID, revision, operationID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{profile, workspaceID, environmentID, revision, operationID}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func (d *Daemon) archiveEnvironmentOperation(ctx context.Context, path, revision, operationID string) worktreeArchiveResult {
	result := worktreeArchiveResult{}
	owner, release, reason := d.lockArchiveEnvironment(ctx, path)
	if reason != "" {
		result.Reason = reason
		return result
	}
	defer release()
	result.WorkspaceID, result.TaskID = owner.WorkspaceID, owner.TaskID
	result.EnvironmentID = d.managedEnvironmentID(path, owner.WorkspaceID, owner.TaskID)
	unlockRepositories, err := execenv.LockEnvironmentArchiveRepositories(ctx, path)
	if err != nil {
		result.Reason = "repository_busy"
		return result
	}
	defer unlockRepositories()
	result.Revision, err = execenv.EnvironmentArchiveRevision(ctx, path)
	if err != nil {
		result.Reason = "archive_unavailable"
		return result
	}
	result.OriginalBytes = dirSize(path)
	if revision == "" {
		return result
	}
	if revision != result.Revision {
		result.Reason = "preview_changed"
		return result
	}
	if free, err := environmentFreeBytes(d.cfg.WorkspacesRoot); err != nil {
		result.Reason = "disk_space_unavailable"
		return result
	} else if free != nil && *free < 64<<20 {
		result.Reason = "insufficient_archive_space"
		return result
	}
	result.ArchiveID = archiveOperationID(d.cfg.Profile, owner.WorkspaceID, result.EnvironmentID, revision, operationID)
	caches, _, err := d.scanWorktreeCaches(ctx, path)
	if err != nil {
		result.Reason = "archive_unavailable"
		return result
	}
	excluded := make([]string, 0, len(caches))
	for _, cache := range caches {
		excluded = append(excluded, filepath.FromSlash(cache.Path))
	}
	manifest, err := execenv.CaptureEnvironmentArchive(ctx, execenv.EnvironmentArchiveRequest{
		WorkspacesRoot: d.cfg.WorkspacesRoot, EnvRoot: path, Profile: d.cfg.Profile, BackendURL: d.cfg.ServerBaseURL,
		ID: result.ArchiveID, Revision: revision, ExcludedCaches: excluded,
	})
	if err != nil {
		result.Reason = "archive_failed"
		return result
	}
	result.ArchiveBytes = manifest.PayloadBytes
	if err := execenv.ArchiveReviewDirectory(ctx, d.cfg.WorkspacesRoot, path); err != nil {
		result.Reason = "review_archive_failed"
		return result
	}
	status, err := d.environmentTaskGCStatus(ctx, path, owner, nil)
	if err != nil || !isAgentTaskTerminal(status.Status) {
		result.Reason = "active"
		return result
	}
	if !validScopedTaskStatus(ctx, owner, status) {
		result.Reason = "scope_changed"
		return result
	}
	if scope, ok := ctx.Value(environmentScopeContextKey{}).(environmentOperationScope); ok && scope.Automatic {
		meta, err := execenv.ReadGCMeta(path)
		if err != nil {
			result.Reason = "unavailable"
			return result
		}
		if eligible, reason := d.automaticArchiveEligible(ctx, path, meta); !eligible {
			result.Reason = reason
			return result
		}
	}
	if active, err := localreview.HasActiveReview(ctx, path, time.Now()); err != nil || active {
		result.Reason = "review"
		return result
	}
	if err := execenv.ReclaimEnvironmentArchive(ctx, d.cfg.WorkspacesRoot, result.ArchiveID); err != nil {
		d.logger.Warn("archive reclamation retained recovery data", "task_id", owner.TaskID, "error", err)
		result.Reason = "reclaim_failed"
		return result
	}
	result.Reclaimed = true
	return result
}

func (d *Daemon) restoreEnvironmentOperation(ctx context.Context, id, workspaceID string) worktreeArchiveResult {
	result := worktreeArchiveResult{ArchiveID: id}
	manifest, err := execenv.ReadEnvironmentArchive(d.cfg.WorkspacesRoot, id)
	if err != nil || manifest.Profile != d.cfg.Profile || manifest.BackendURL != d.cfg.ServerBaseURL || (workspaceID != "" && workspaceID != manifest.Owner.WorkspaceID) {
		result.Reason = "unowned"
		return result
	}
	result.WorkspaceID, result.TaskID = manifest.Owner.WorkspaceID, manifest.Owner.TaskID
	path := filepath.Join(d.cfg.WorkspacesRoot, filepath.FromSlash(manifest.RelativeRoot))
	result.EnvironmentID = d.managedEnvironmentID(path, manifest.Owner.WorkspaceID, manifest.Owner.TaskID)
	release, ok := d.reserveEnvRootForGC(path)
	if !ok {
		result.Reason = "active"
		return result
	}
	defer release()
	if d.client == nil {
		result.Reason = "unavailable"
		return result
	}
	status, err := d.environmentTaskGCStatus(ctx, path, &manifest.Owner, manifest.Metadata)
	if err != nil {
		result.Reason = "unavailable"
		return result
	}
	if !validScopedTaskStatus(ctx, &manifest.Owner, status) {
		result.Reason = "scope_changed"
		return result
	}
	if !isAgentTaskTerminal(status.Status) {
		result.Reason = "active"
		return result
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		result.Reason = "existing_environment"
		return result
	}
	if _, err := execenv.RestoreEnvironmentArchive(ctx, d.cfg.WorkspacesRoot, id, d.cfg.Profile, d.cfg.ServerBaseURL); err != nil {
		result.Reason = "restore_failed"
		return result
	}
	result.Restored = true
	return result
}

func (d *Daemon) listEnvironmentArchives(ctx context.Context, workspaceID string) ([]worktreeArchiveSummary, error) {
	rows := []worktreeArchiveSummary{}
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(".environment-archive")
	if errors.Is(err, os.ErrNotExist) {
		return rows, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&linkedDirModes != 0 {
		return nil, errors.New("archive inventory unavailable")
	}
	parent, err := root.OpenRoot(".environment-archive")
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	entries, err := os.ReadDir(parent.Name())
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || entry.Type()&linkedDirModes != 0 || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		manifest, err := execenv.ReadEnvironmentArchive(d.cfg.WorkspacesRoot, entry.Name())
		if err != nil {
			continue
		}
		if manifest.Profile != d.cfg.Profile || manifest.BackendURL != d.cfg.ServerBaseURL || (workspaceID != "" && manifest.Owner.WorkspaceID != workspaceID) {
			continue
		}
		row := worktreeArchiveSummary{ArchiveID: manifest.ID, WorkspaceID: manifest.Owner.WorkspaceID, TaskID: manifest.Owner.TaskID,
			TaskName: filepath.Base(filepath.FromSlash(manifest.RelativeRoot)), Kind: "unknown", CreatedAt: manifest.CreatedAt,
			OriginalPath: filepath.Join(d.cfg.WorkspacesRoot, filepath.FromSlash(manifest.RelativeRoot)), ArchiveBytes: manifest.PayloadBytes, LogicalBytes: manifest.LogicalBytes}
		if meta := manifest.Metadata; meta != nil {
			row.AgentID, row.AgentName, row.RuntimeID = meta.AgentID, meta.AgentName, meta.RuntimeID
			row.ProjectID, row.ProjectName, row.SquadID, row.SquadName = meta.ProjectID, meta.ProjectName, meta.SquadID, meta.SquadName
			row.Kind = string(meta.Kind)
		}
		if _, err := root.Lstat(filepath.FromSlash(manifest.RelativeRoot)); !errors.Is(err, os.ErrNotExist) {
			row.RestoreReason = "existing_environment"
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (d *Daemon) manageEnvironmentArchives(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := d.listEnvironmentArchives(r.Context(), r.URL.Query().Get("workspace_id"))
		if err != nil {
			http.Error(w, "archive inventory unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			d.logger.Debug("archive inventory response interrupted", "error", err)
		}
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request worktreeArchiveRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || len(request.Selections) > 1000 || (request.Action != "preview" && request.Action != "archive" && request.Action != "restore") {
		http.Error(w, "invalid archive operation", http.StatusBadRequest)
		return
	}
	if request.Action == "restore" {
		if len(request.ArchiveID) != 64 || len(request.Selections) != 0 {
			http.Error(w, "archive id required", http.StatusBadRequest)
			return
		}
		result := d.restoreEnvironmentOperation(r.Context(), request.ArchiveID, request.WorkspaceID)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode([]worktreeArchiveResult{result}); err != nil {
			d.logger.Debug("archive restore response interrupted", "error", err)
		}
		return
	}
	if request.Action == "archive" && (len(request.OperationID) != 64 || len(request.Selections) == 0) {
		http.Error(w, "operation id and selections required", http.StatusBadRequest)
		return
	}
	for _, selection := range request.Selections {
		if len(selection.EnvironmentID) != 64 || (request.Action == "archive" && len(selection.Revision) != 64) {
			http.Error(w, "environment id and preview revision required", http.StatusBadRequest)
			return
		}
	}
	report, err := ScanDiskUsage(d.cfg.WorkspacesRoot, d.cfg.GCArtifactPatterns)
	if err != nil {
		http.Error(w, "inventory unavailable", http.StatusServiceUnavailable)
		return
	}
	known := make(map[string]string)
	for _, row := range report.Tasks {
		owner, err := d.gcTaskDirOwner(row.Path)
		if err == nil && (request.WorkspaceID == "" || request.WorkspaceID == owner.WorkspaceID) {
			known[d.managedEnvironmentID(row.Path, owner.WorkspaceID, owner.TaskID)] = row.Path
		}
	}
	selections := request.Selections
	if len(selections) == 0 {
		for _, row := range report.Tasks {
			owner, err := d.gcTaskDirOwner(row.Path)
			if err == nil {
				id := d.managedEnvironmentID(row.Path, owner.WorkspaceID, owner.TaskID)
				if _, ok := known[id]; ok {
					selections = append(selections, worktreeArchiveSelection{EnvironmentID: id})
				}
			}
		}
	}
	results := []worktreeArchiveResult{}
	seen := make(map[string]bool)
	for _, selection := range selections {
		if seen[selection.EnvironmentID] {
			continue
		}
		seen[selection.EnvironmentID] = true
		path, ok := known[selection.EnvironmentID]
		if !ok {
			results = append(results, worktreeArchiveResult{EnvironmentID: selection.EnvironmentID, Reason: "unowned"})
			continue
		}
		revision := ""
		if request.Action == "archive" {
			revision = selection.Revision
		}
		result := d.archiveEnvironmentOperation(r.Context(), path, revision, request.OperationID)
		result.EnvironmentID = selection.EnvironmentID
		results = append(results, result)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		d.logger.Debug("archive operation response interrupted", "error", err)
	}
}
