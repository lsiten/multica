package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// automaticCleanupEligible releases environments once no current task needs
// their code. Saved history and an idle chat are not checkout consumers.
func (d *Daemon) automaticCleanupEligible(ctx context.Context, path string, meta *execenv.GCMeta) (bool, string) {
	if !d.cfg.EnvironmentRecycleEnabled || !d.cfg.GCEnabled || d.cfg.KeepEnvAfterTask {
		return false, "disabled"
	}
	if d.isActiveEnvRoot(path) {
		return false, "active"
	}
	owner, err := d.gcTaskDirOwner(path)
	if err != nil || d.client == nil {
		return false, "unowned"
	}
	status, err := d.environmentTaskGCStatus(ctx, path, owner, meta)
	if errors.Is(err, errEnvironmentTaskScopeChanged) {
		return false, "scope_changed"
	}
	if err != nil {
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
	if reason := d.environmentRetentionReason(ctx, path, status); reason != "" {
		return false, reason
	}
	return true, ""
}

func (d *Daemon) scheduleAutomaticEnvironmentRecycle(ctx context.Context, path string) bool {
	if !d.automaticEnvironmentCapacityAvailable() {
		return false
	}
	meta, err := d.reconcileEnvironmentMetadata(ctx, path)
	if err != nil {
		return false
	}
	eligible, reason := d.automaticCleanupEligible(ctx, path, meta)
	if !eligible {
		d.logger.Debug("automatic environment recycling retained root", "directory", filepath.Base(path), "reason", reason)
		return false
	}
	preview := d.cleanupUnreferencedEnvironment(ctx, path, "")
	if preview.Reason != "" || preview.EnvironmentID == "" || preview.Revision == "" {
		return false
	}
	// A failed attempt may be retried next hour with its old receipt retained.
	// Within the same window repeated GC scans and completion callbacks dedupe.
	digest := sha256.Sum256([]byte("automatic-cleanup\x00" + preview.EnvironmentID + "\x00" + preview.Revision + "\x00" + time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339)))
	operation := protocol.EnvironmentOperationRequest{ID: hex.EncodeToString(digest[:]), Action: "cleanup", Selections: []protocol.EnvironmentSelection{{EnvironmentID: preview.EnvironmentID, Revision: preview.Revision}}}
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
	if !d.cfg.GCEnabled || !d.cfg.EnvironmentRecycleEnabled || d.cfg.KeepEnvAfterTask {
		return
	}
	interval := d.cfg.EnvironmentRecycleInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	d.scanAutomaticEnvironmentRecycle(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	capacityTicker := time.NewTicker(5 * time.Minute)
	defer capacityTicker.Stop()
	var changed <-chan struct{}
	if d.environmentChanges != nil {
		changed = d.environmentChanges.notify()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.scanAutomaticEnvironmentRecycle(ctx)
		case <-changed:
			changed = d.environmentChanges.notify()
			d.scanAutomaticEnvironmentRecycle(ctx)
		case <-capacityTicker.C:
			capacityCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			d.scanEnvironmentCapacity(capacityCtx)
			cancel()
		}
	}
}

func (d *Daemon) scanAutomaticEnvironmentRecycle(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	d.resumeInterruptedEnvironmentRemovals(ctx)
	d.cleanupObsoleteEnvironmentArchives(ctx)
	paths, err := d.environmentRootPaths(ctx)
	if err != nil {
		return
	}
	ctx = d.prefetchEnvironmentLifecycles(ctx, paths)
	ctx = d.withEnvironmentReviewReferences(ctx, paths)
	type archiveBatch struct {
		scope      environmentOperationScope
		selections []protocol.EnvironmentSelection
	}
	groups := map[environmentOperationScope]*archiveBatch{}
	reasons := map[string]int{}
	for _, path := range paths {
		if ctx.Err() != nil {
			break
		}
		if d.isActiveEnvRoot(path) {
			reasons["active"]++
			continue
		}
		meta, err := d.reconcileEnvironmentMetadata(ctx, path)
		if err != nil {
			reasons["metadata_unavailable"]++
			continue
		}
		eligible, reason := d.automaticCleanupEligible(ctx, path, meta)
		if !eligible {
			reasons[reason]++
			continue
		}
		owner, err := d.gcTaskDirOwner(path)
		if err != nil {
			reasons["unowned"]++
			continue
		}
		status, err := d.environmentTaskGCStatus(ctx, path, owner, meta)
		if err != nil {
			reasons["unavailable"]++
			continue
		}
		preview := d.cleanupUnreferencedEnvironment(ctx, path, "")
		if preview.Reason != "" {
			reasons[preview.Reason]++
			continue
		}
		scope := environmentOperationScope{WorkspaceID: owner.WorkspaceID, RuntimeID: status.RuntimeID, Automatic: true}
		batch := groups[scope]
		if batch == nil {
			batch = &archiveBatch{scope: scope}
			groups[scope] = batch
		}
		batch.selections = append(batch.selections, protocol.EnvironmentSelection{EnvironmentID: preview.EnvironmentID, Revision: preview.Revision})
	}
	scheduled := 0
	for _, batch := range groups {
		for offset := 0; offset < len(batch.selections); offset += 1000 {
			selections := batch.selections[offset:min(offset+1000, len(batch.selections))]
			data, err := json.Marshal(selections)
			if err != nil {
				reasons["invalid_selection"]++
				continue
			}
			digest := sha256.Sum256(append(data, []byte(time.Now().UTC().Truncate(time.Minute).Format(time.RFC3339))...))
			started, err := d.startEnvironmentOperation(batch.scope, protocol.EnvironmentOperationRequest{ID: hex.EncodeToString(digest[:]), Action: "cleanup", Selections: selections})
			if err != nil {
				reasons["operation_unavailable"] += len(selections)
				continue
			}
			if started.Status == "running" {
				scheduled += len(selections)
			}
		}
	}
	d.logger.Info("environment task reconciliation complete", "directories", len(paths), "scheduled", scheduled, "retained_by_reason", reasons)
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
		d.logger.Info("automatic environment cleanup scheduled", "directory", filepath.Base(path))
	}
	stats.skipped++
}
