package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) recoverRemoteMerge(ctx context.Context, input worktreeReviewRequest, claim string) (protocol.LocalReviewResult, bool) {
	result := protocol.LocalReviewResult{ClaimToken: claim}
	if err := localReviewOperations.Lock(ctx); err != nil {
		result.Error = err.Error()
		return result, true
	}
	defer localReviewOperations.Unlock()
	path, root, err := d.resolveReviewRoot(ctx, input)
	if err != nil {
		result.Error = err.Error()
		return result, true
	}
	release, available := d.reserveEnvRootForGC(root)
	if !available {
		result.Error = "task environment is busy; retry recovery when idle"
		return result, true
	}
	defer release()
	workspace, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		result.Error = "workspace unavailable"
		return result, true
	}
	defer workspace.Close()
	relative, err := filepath.Rel(d.cfg.WorkspacesRoot, root)
	if err != nil {
		result.Error = "invalid task environment"
		return result, true
	}
	envClaim, _, err := execenv.LockEnvRootForReuse(workspace, relative, root)
	if err != nil || envClaim == nil {
		result.Error = "task environment is busy"
		return result, true
	}
	defer envClaim.Release()
	if input.legacyRuntime != nil {
		if err := persistLegacyReviewRuntime(root, *input.legacyRuntime); err != nil {
			result.Error = err.Error()
			return result, true
		}
	}
	releaseSource, err := d.claimReviewSource(ctx, path, root)
	if err != nil {
		result.Error = err.Error()
		return result, true
	}
	defer releaseSource()
	unlock, err := execenv.LockRepositoryForReview(path)
	if err != nil {
		result.Error = "repository is busy"
		return result, true
	}
	defer unlock()
	key := localreview.RecordKey(localreview.Snapshot{Path: path, Target: input.Target})
	record, err := localreview.LoadRecord(root, key)
	if err != nil {
		result.Error = "cannot read merge recovery record"
		return result, true
	}
	if record.SnapshotID != input.SnapshotID || record.PreparedCommit == "" || record.Snapshot == nil {
		return result, false
	}
	if record.CommandID == input.CommandID {
		prepared := record.PreparedRequest
		if prepared == nil || prepared.ActorID != input.ActorID || prepared.Comment != input.Comment || prepared.Kind != "merge" || prepared.SnapshotID != input.SnapshotID {
			result.Error = "merge operation ID already used with different or unverifiable request data"
			return result, true
		}
	}
	if !localreview.ContainsMerge(ctx, *record.Snapshot, record.PreparedCommit) {
		// A fresh user-requested command can retry a failed precondition after
		// it is fixed. Redelivery of the same command never repeats a write.
		if input.CommandID != "" && input.CommandID != record.CommandID && localreview.TargetUnchanged(ctx, *record.Snapshot) {
			record.PreparedCommit, record.Snapshot, record.CommandID = "", nil, ""
			record.PreparedRequest = nil
			if err := localreview.SaveRecord(root, key, record); err != nil {
				result.Error = "cannot clear failed merge preparation"
				return result, true
			}
			return result, false
		}
		result.Error = "previous merge was prepared but is not present on target; inspect and refresh before retrying"
		return result, true
	}
	if record.State != "merged" || record.MergedCommit != record.PreparedCommit {
		record.State, record.MergedCommit = "merged", record.PreparedCommit
		event := localreview.Event{SnapshotID: record.SnapshotID}
		if record.PreparedRequest != nil {
			event = *record.PreparedRequest
		}
		event.Kind, event.CreatedAt = "merge_recovered", time.Now().UTC()
		record.Events = append(record.Events, event)
		if err := localreview.SaveRecord(root, key, record); err != nil {
			result.Error = "cannot persist recovered merge receipt"
			return result, true
		}
	}
	if input.VersionID != "" {
		if record.VersionID != input.VersionID {
			result.Error = "merge recovery version mismatch"
			return result, true
		}
		store, openErr := localreview.OpenBlobStore(root, 64<<20)
		if openErr != nil {
			result.Error = "merge completed but review cache is unavailable"
			return result, true
		}
		defer store.Close()
		version, loadErr := store.LoadVersion(ctx, input.VersionID)
		if loadErr != nil {
			result.Error = "merge completed but review version is unavailable"
			return result, true
		}
		page, pageErr := version.FilePage(localreview.FilePageRequest{Limit: 100})
		if pageErr != nil {
			result.Error = pageErr.Error()
			return result, true
		}
		result.Page, err = json.Marshal(pagedReviewManifest{VersionID: input.VersionID, Header: version.Header, Page: page, Review: record.View()})
		if err != nil {
			result.Error = "cannot encode recovered review version"
		}
		result.MergedCommit = record.PreparedCommit
		return result, true
	}
	result.Snapshot, err = json.Marshal(record.Snapshot)
	if err != nil {
		result.Error = "cannot encode recovered snapshot"
		return result, true
	}
	result.MergedCommit = record.PreparedCommit
	result.Review, err = json.Marshal(record.View())
	if err != nil {
		result.Error = "cannot encode recovered review receipt"
	}
	return result, true
}
