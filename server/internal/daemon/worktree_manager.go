package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// ManagedWorktree is a daemon-owned completed task environment. Its Path is
// intentionally local-only: the health listener binds to loopback and the
// desktop app needs it only as an opaque cleanup handle.
type ManagedWorktree struct {
	TaskDiskUsage
	Active           bool   `json:"active"`
	ProtectionReason string `json:"protection_reason"`
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
		switch r.Method {
		case http.MethodGet:
			d.writeManagedWorktrees(w, r)
		case http.MethodDelete:
			d.deleteManagedWorktrees(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func (d *Daemon) managedWorktrees(ctx context.Context) ([]ManagedWorktree, error) {
	report, err := ScanDiskUsage(d.cfg.WorkspacesRoot, d.cfg.GCArtifactPatterns)
	if err != nil {
		return nil, err
	}
	worktrees := make([]ManagedWorktree, 0, len(report.Tasks))
	for _, task := range report.Tasks {
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
		if task.AgentID == "" {
			if provenance, err := execenv.ReadManagedEnvProvenance(task.Path); err == nil {
				task.AgentID = provenance.AgentID
				task.AgentName = provenance.AgentName
			}
		}
		worktrees = append(worktrees, ManagedWorktree{
			TaskDiskUsage:    task,
			Active:           active,
			ProtectionReason: reason,
		})
	}
	return worktrees, nil
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
