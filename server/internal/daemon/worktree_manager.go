package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ManagedWorktree is a daemon-owned completed task environment. Its Path is
// intentionally local-only: the health listener binds to loopback and the
// desktop app needs it only as an opaque cleanup handle.
type ManagedWorktree struct {
	TaskDiskUsage
	protocol.WorktreeLifecycle
	Active           bool                  `json:"active"`
	ProtectionReason string                `json:"protection_reason"`
	TaskID           string                `json:"task_id"`
	RuntimeID        string                `json:"runtime_id,omitempty"`
	EnvironmentID    string                `json:"environment_id"`
	Repositories     []string              `json:"repositories"`
	EnvironmentKind  string                `json:"environment_kind"`
	Storage          *worktreeStorageUsage `json:"storage,omitempty"`
	ProjectID        string                `json:"project_id,omitempty"`
	ProjectName      string                `json:"project_name,omitempty"`
	SquadID          string                `json:"squad_id,omitempty"`
	SquadName        string                `json:"squad_name,omitempty"`
}

type managedWorktreeCleanupRequest struct {
	Paths          []string `json:"paths"`
	DiscardChanges bool     `json:"discard_changes"`
}

type managedWorktreeCleanupResponse struct {
	RemovedPaths []string          `json:"removed_paths"`
	BusyPaths    []string          `json:"busy_paths"`
	Retained     map[string]string `json:"retained"`
}

// worktreeManagerHandler exposes the daemon's task environments to its local
// desktop owner. Cleanup always re-discovers the requested paths and reserves
// each root first, so a stale UI can never remove a task that has started.
func (d *Daemon) worktreeManagerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.client == nil || d.client.Token() == "" || r.Header.Get("Origin") != "" || r.Header.Get("X-Multica-Profile") != d.cfg.Profile ||
			subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+d.client.Token())) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/worktrees/archives" {
			d.manageEnvironmentArchives(w, r)
			return
		}
		if r.URL.Path == "/worktrees/operations" {
			d.environmentOperationsHandler(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			d.writeManagedWorktrees(w, r)
		case http.MethodDelete:
			d.deleteManagedWorktrees(w, r)
		case http.MethodPost:
			d.manageWorktreeResources(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func (d *Daemon) managedWorktrees(ctx context.Context) ([]ManagedWorktree, error) {
	return d.managedWorktreesForPaths(ctx, nil)
}

func (d *Daemon) managedWorktreesForPaths(ctx context.Context, allowed map[string]bool) ([]ManagedWorktree, error) {
	report, err := ScanDiskUsage(d.cfg.WorkspacesRoot, d.cfg.GCArtifactPatterns)
	if err != nil {
		return nil, err
	}
	worktrees := make([]ManagedWorktree, 0, len(report.Tasks))
	allocatedFiles := make(map[string]bool)
	for _, task := range report.Tasks {
		if allowed != nil && !allowed[task.Path] {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		reason := ""
		active := d.isActiveEnvRoot(task.Path)
		if active {
			reason = "active"
		} else if _, err := d.gcTaskDirOwner(task.Path); err != nil {
			reason = "unowned"
		} else {
			_, reason = inspectWorktreeRepositories(ctx, task.Path, d.cfg.WorkspacesRoot)
		}
		if !active && reason != "unowned" {
			if reviewing, err := localreview.HasActiveReview(ctx, task.Path, time.Now()); err != nil {
				reason = "unavailable"
			} else if reviewing {
				reason = "review"
			}
		}
		if task.AgentID == "" {
			if provenance, err := execenv.ReadManagedEnvProvenance(task.Path); err == nil {
				task.AgentID = provenance.AgentID
				task.AgentName = provenance.AgentName
			}
		}
		taskID := ""
		runtimeID := ""
		if owner, err := d.gcTaskDirOwner(task.Path); err == nil {
			taskID = owner.TaskID
			if binding, err := execenv.ReadReviewRuntime(task.Path); err == nil && binding.TaskID == owner.TaskID && binding.WorkspaceID == owner.WorkspaceID {
				runtimeID = binding.RuntimeID
				if task.AgentID == "" {
					task.AgentID, task.AgentName = binding.AgentID, binding.AgentName
				}
			}
		}
		repositories, _ := inspectWorktreeRepositories(ctx, task.Path, d.cfg.WorkspacesRoot)
		if len(repositories) == 0 && taskID != "" {
			binding, err := execenv.ReadReviewDirectory(task.Path)
			if err == nil && binding.SourcePath == "" && binding.Commit == "" && binding.TaskID == taskID && binding.WorkspaceID == task.WorkspaceID && filepath.IsAbs(binding.Path) {
				canonical, err := filepath.EvalSymlinks(binding.Path)
				if err == nil && canonical == binding.Path {
					repositories = []string{canonical}
				}
			}
		}
		if repositories == nil {
			repositories = []string{}
		}
		row := ManagedWorktree{
			TaskDiskUsage:    task,
			Active:           active,
			ProtectionReason: reason,
			TaskID:           taskID,
			RuntimeID:        runtimeID,
			Repositories:     repositories,
			EnvironmentID:    d.managedEnvironmentID(task.Path, task.WorkspaceID, taskID),
		}
		row.EnvironmentKind = "directory"
		if len(repositories) > 0 {
			row.EnvironmentKind = "git_worktree"
		}
		if usage, err := scanWorktreeStorage(ctx, task.Path, allocatedFiles); err == nil {
			row.Storage = &usage
		}
		if meta, err := execenv.ReadGCMeta(task.Path); err == nil && meta.WorkspaceID == task.WorkspaceID && meta.TaskID == taskID {
			row.ProjectID, row.ProjectName = meta.ProjectID, meta.ProjectName
			row.SquadID, row.SquadName = meta.SquadID, meta.SquadName
			if row.RuntimeID == "" {
				row.RuntimeID = meta.RuntimeID
			}
		}
		worktrees = append(worktrees, row)
	}
	d.describeManagedWorktreeInventory(ctx, worktrees)
	return worktrees, nil
}

// Lifecycle requests share a bounded pool and start after local disk inspection.
// A large scan must not exhaust the lifecycle deadline before the first lookup.
func (d *Daemon) describeManagedWorktreeInventory(ctx context.Context, rows []ManagedWorktree) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	queue := make(chan int, len(rows))
	for index := range rows {
		queue <- index
	}
	close(queue)
	var workers sync.WaitGroup
	for range min(8, len(rows)) {
		workers.Go(func() {
			for index := range queue {
				d.describeManagedWorktree(ctx, &rows[index])
			}
		})
	}
	workers.Wait()
}

func (d *Daemon) writeManagedWorktrees(w http.ResponseWriter, r *http.Request) {
	worktrees, err := d.managedWorktrees(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(worktrees)
}

func (d *Daemon) deleteManagedWorktrees(w http.ResponseWriter, r *http.Request) {
	var request managedWorktreeCleanupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || len(request.Paths) == 0 || len(request.Paths) > 1000 {
		http.Error(w, "paths is required", http.StatusBadRequest)
		return
	}
	report, err := ScanDiskUsage(d.cfg.WorkspacesRoot, d.cfg.GCArtifactPatterns)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	known := make(map[string]bool, len(report.Tasks))
	for _, worktree := range report.Tasks {
		known[worktree.Path] = true
	}
	response := managedWorktreeCleanupResponse{RemovedPaths: []string{}, BusyPaths: []string{}, Retained: map[string]string{}}
	seen := make(map[string]bool, len(request.Paths))
	for _, path := range request.Paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if !known[path] {
			response.Retained[path] = "unowned"
			continue
		}
		if reason := d.cleanupManagedWorktree(r.Context(), worktreeCleanup{path: path, discardChanges: request.DiscardChanges}); reason != "" {
			response.Retained[path] = reason
			if reason == "active" {
				response.BusyPaths = append(response.BusyPaths, path)
			}
			continue
		}
		response.RemovedPaths = append(response.RemovedPaths, path)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
