package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// automaticArchiveEligible uses completed run evidence and the parent lifecycle.
// An active chat remains resumable; archived chats and completed automation runs
// release their working environments without deleting their saved history.
func (d *Daemon) automaticArchiveEligible(ctx context.Context, path string, meta *execenv.GCMeta) (bool, string) {
	if !d.cfg.EnvironmentArchiveEnabled || !d.cfg.GCEnabled || d.cfg.KeepEnvAfterTask {
		return false, "disabled"
	}
	if d.isActiveEnvRoot(path) {
		return false, "active"
	}
	if meta == nil || meta.CompletedAt.IsZero() {
		return false, "unknown_completion"
	}
	owner, err := d.gcTaskDirOwner(path)
	if err != nil || d.client == nil {
		return false, "unowned"
	}
	if meta.WorkspaceID != owner.WorkspaceID {
		return false, "scope_changed"
	}
	status, err := d.environmentTaskGCStatus(ctx, path, owner, meta)
	if errors.Is(err, errEnvironmentTaskScopeChanged) {
		return false, "scope_changed"
	}
	if err != nil || !isAgentTaskTerminal(status.Status) {
		return false, "unavailable"
	}
	if !status.LifecycleSupported || status.WorkspaceID == "" {
		return false, "unavailable"
	}
	if !validScopedTaskStatus(ctx, owner, status) || status.WorkspaceID != owner.WorkspaceID {
		return false, "scope_changed"
	}
	policy, err := d.loadEnvironmentPolicy(environmentOperationScope{WorkspaceID: owner.WorkspaceID, RuntimeID: status.RuntimeID})
	if err != nil {
		return false, "policy_unavailable"
	}
	if !policy.Enabled {
		return false, "disabled"
	}
	if status.ChatSessionID != "" && (meta.Kind != execenv.GCKindChat || meta.ChatSessionID != status.ChatSessionID) {
		return false, "scope_changed"
	}
	if status.ChatSessionID == "" && status.AutopilotRunID != "" && meta.Kind != execenv.GCKindAutopilotRun && meta.Kind != execenv.GCKindProjectSupervision {
		return false, "scope_changed"
	}
	completed := meta.CompletedAt
	restored, err := execenv.EnvironmentRestoredAt(path)
	if err != nil {
		return false, "unknown_restoration"
	}
	if restored.After(completed) {
		completed = restored
	}
	if status.CompletedAt.After(completed) {
		completed = status.CompletedAt
	}
	switch meta.Kind {
	case execenv.GCKindChat:
		if meta.ChatSessionID == "" || status.ChatSessionID != meta.ChatSessionID {
			return false, "unknown_parent"
		}
		chat, err := d.client.GetChatSessionGCCheck(ctx, meta.ChatSessionID)
		if err != nil {
			return false, "unavailable"
		}
		if chat.Status != "archived" && chat.Status != "expired" {
			return false, "active_chat"
		}
		if chat.UpdatedAt.After(completed) {
			completed = chat.UpdatedAt
		}
	case execenv.GCKindAutopilotRun:
		if meta.AutopilotRunID == "" || status.AutopilotRunID != meta.AutopilotRunID {
			return false, "unknown_parent"
		}
	case execenv.GCKindIssue, execenv.GCKindQuickCreate:
		if meta.IssueID != "" && meta.IssueID != status.IssueID {
			return false, "unknown_parent"
		}
	case execenv.GCKindProjectSupervision:
	default:
		return false, "unknown_kind"
	}
	if status.IssueID != "" {
		if status.IssueStatusCategory != issuestatus.CategoryDone && status.IssueStatusCategory != issuestatus.CategoryClosed {
			return false, "issue_active"
		}
		if status.LastActivityAt != nil && status.LastActivityAt.After(completed) {
			completed = *status.LastActivityAt
		}
	}
	if status.AutopilotRunID != "" {
		run, err := d.client.GetAutopilotRunGCCheck(ctx, status.AutopilotRunID)
		if err != nil || !isAutopilotRunTerminal(run.Status) {
			return false, "automation_active"
		}
		if run.CompletedAt.After(completed) {
			completed = run.CompletedAt
		}
	}
	if time.Since(completed) < time.Duration(policy.ArchiveAfterHours)*time.Hour {
		return false, "retention"
	}
	return true, ""
}

func (d *Daemon) scheduleAutomaticEnvironmentRecycle(ctx context.Context, path string) bool {
	if !d.automaticEnvironmentCapacityAvailable() {
		return false
	}
	meta, err := execenv.ReadGCMeta(path)
	if err != nil {
		return false
	}
	eligible, reason := d.automaticArchiveEligible(ctx, path, meta)
	if !eligible {
		d.logger.Debug("automatic environment recycling retained root", "task_id", meta.TaskID, "reason", reason)
		return false
	}
	preview := d.archiveEnvironmentOperation(ctx, path, "", "")
	if preview.Reason != "" || preview.EnvironmentID == "" || preview.Revision == "" {
		return false
	}
	// A failed attempt may be retried next hour with its old receipt retained.
	// Within the same window repeated GC scans and completion callbacks dedupe.
	digest := sha256.Sum256([]byte("automatic-archive\x00" + preview.EnvironmentID + "\x00" + preview.Revision + "\x00" + time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339)))
	operation := protocol.EnvironmentOperationRequest{ID: hex.EncodeToString(digest[:]), Action: "archive", Selections: []protocol.EnvironmentSelection{{EnvironmentID: preview.EnvironmentID, Revision: preview.Revision}}}
	owner, err := d.gcTaskDirOwner(path)
	if err != nil {
		return false
	}
	status, err := d.environmentTaskGCStatus(ctx, path, owner, meta)
	if err != nil || status.WorkspaceID != "" && status.WorkspaceID != preview.WorkspaceID {
		return false
	}
	started, err := d.startEnvironmentOperation(environmentOperationScope{WorkspaceID: preview.WorkspaceID, RuntimeID: status.RuntimeID, Automatic: true}, operation)
	if err != nil {
		d.logger.Warn("automatic environment recycling could not start", "task_id", preview.TaskID, "error", err)
		return false
	}
	return started.Status == "running"
}

// Parent events can occur after the task worker has exited. A separate bounded
// scan catches archived chats and completed automations without waiting for GC.
func (d *Daemon) environmentRecycleLoop(ctx context.Context) {
	if !d.cfg.GCEnabled || !d.cfg.EnvironmentArchiveEnabled || d.cfg.KeepEnvAfterTask {
		return
	}
	interval := d.cfg.EnvironmentRecycleInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.scanAutomaticEnvironmentRecycle(ctx)
		}
	}
}

func (d *Daemon) scanAutomaticEnvironmentRecycle(ctx context.Context) {
	ctx, cancelScan := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelScan()
	capacityCtx, cancelCapacity := context.WithTimeout(ctx, 30*time.Second)
	d.scanEnvironmentCapacity(capacityCtx)
	cancelCapacity()
	queue := make(chan string, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for path := range queue {
				if ctx.Err() != nil {
					return
				}
				checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				d.scheduleAutomaticEnvironmentRecycle(checkCtx, path)
				cancel()
			}
		})
	}
	defer workers.Wait()
	defer close(queue)
	workspaces, err := os.ReadDir(d.cfg.WorkspacesRoot)
	if err != nil {
		return
	}
	for _, workspace := range workspaces {
		if !workspace.IsDir() || strings.HasPrefix(workspace.Name(), ".") {
			continue
		}
		parent := filepath.Join(d.cfg.WorkspacesRoot, workspace.Name())
		roots, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, root := range roots {
			if ctx.Err() != nil {
				return
			}
			if !root.IsDir() || strings.HasPrefix(root.Name(), ".") {
				continue
			}
			select {
			case queue <- filepath.Join(parent, root.Name()):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (d *Daemon) automaticRecycleWorkspace(ctx context.Context, path string, meta *execenv.GCMeta, stats *gcStats) {
	// Cache GC retains the source and is independent of archive eligibility.
	cacheTTL := d.cfg.GCArtifactTTL
	if owner, err := d.gcTaskDirOwner(path); err == nil {
		if status, err := d.environmentTaskGCStatus(ctx, path, owner, meta); err == nil && status.WorkspaceID == owner.WorkspaceID {
			policy, err := d.loadEnvironmentPolicy(environmentOperationScope{WorkspaceID: owner.WorkspaceID, RuntimeID: status.RuntimeID})
			if err != nil || !policy.Enabled {
				stats.skipped++
				return
			}
			cacheTTL = time.Duration(policy.CacheAfterHours) * time.Hour
		} else {
			stats.skipped++
			return
		}
	} else {
		stats.skipped++
		return
	}
	if meta != nil && !meta.CompletedAt.IsZero() && cacheTTL > 0 && time.Since(meta.CompletedAt) >= cacheTTL {
		preview := d.worktreeCacheOperation(ctx, path, "")
		if preview.Reason == "" && len(preview.Candidates) > 0 {
			result := d.worktreeCacheOperation(ctx, path, preview.Revision)
			recordArtifactCleanup(stats, result.RemovedCount, result.RemovedBytes, map[string]int{"verified_environment_cache": result.RemovedCount})
		}
	}
	if d.scheduleAutomaticEnvironmentRecycle(ctx, path) {
		d.logger.Info("automatic environment archival scheduled", "directory", filepath.Base(path))
	}
	stats.skipped++
}
