package daemon

import (
	"context"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) describeManagedWorktree(ctx context.Context, row *ManagedWorktree) {
	row.WorktreeLifecycle = protocol.WorktreeLifecycle{NextAction: protocol.WorktreeUnknown, RepositoriesDetails: []protocol.WorktreeRepositoryLifecycle{}}
	if row.Active {
		row.NextAction = protocol.WorktreeActive
		return
	}
	if row.TaskID == "" || d.client == nil || row.ProtectionReason == "unowned" || row.ProtectionReason == "unavailable" {
		return
	}
	owner, err := d.gcTaskDirOwner(row.Path)
	if err != nil {
		return
	}
	status, err := d.environmentTaskGCStatus(ctx, row.Path, owner, nil)
	if err != nil {
		return
	}
	row.RunStatus = status.Status
	row.IssueID, row.IssueStatus, row.IssueStatusCategory = status.IssueID, status.IssueStatus, status.IssueStatusCategory
	row.LastActivityAt = status.LastActivityAt
	if !status.CompletedAt.IsZero() {
		row.CompletedAt = &status.CompletedAt
	}
	switch status.Status {
	case "queued", "dispatched", "running", "waiting_local_directory", "deferred":
		row.Active, row.NextAction, row.ProtectionReason = true, protocol.WorktreeActive, "active"
		return
	case "completed", "failed", "cancelled":
	default:
		return
	}
	if !status.LifecycleSupported || (status.IssueID != "" && !issuestatus.IsCategory(status.IssueStatusCategory)) {
		return
	}
	ttl := d.cfg.WorktreeStaleTTL
	if ttl <= 0 {
		ttl = protocol.DefaultWorktreeStaleTTL
	}
	row.Stale = protocol.WorktreeStale(row.WorktreeLifecycle, time.Now(), ttl)
	if row.ProtectionReason == "review" {
		row.NextAction = protocol.WorktreeReview
		return
	}
	for _, repository := range row.Repositories {
		detail, err := localreview.InspectDecision(ctx, row.Path, repository)
		if err != nil {
			detail = protocol.WorktreeRepositoryLifecycle{Path: repository, NextAction: protocol.WorktreeUnknown, Reason: "unavailable"}
		}
		row.RepositoriesDetails = append(row.RepositoriesDetails, detail)
	}
	row.NextAction = summarizeWorktreeActions(row.RepositoriesDetails)
	if len(row.Repositories) == 0 {
		row.NextAction = protocol.WorktreeCleanup
		if binding, err := execenv.ReadReviewDirectory(row.Path); err == nil {
			// A pinned delivery can outlive its checkout; the destination's HEAD
			// is not the delivered revision and cannot prove it was reviewed.
			if binding.Commit != "" || binding.SourcePath != "" {
				row.NextAction = protocol.WorktreeRetained
			}
		} else if !os.IsNotExist(err) {
			row.NextAction = protocol.WorktreeUnknown
		}
	}
	if reason := worktreeDeliverablesReason(ctx, row.Path, row.Repositories); reason != "" {
		row.ProtectionReason = reason
		if reason == "unavailable" {
			row.NextAction = protocol.WorktreeUnknown
		}
		if row.NextAction == protocol.WorktreeCleanup {
			row.NextAction = protocol.WorktreeRetained
		}
	}
	if row.ProtectionReason != "" && row.NextAction == protocol.WorktreeCleanup {
		row.NextAction = protocol.WorktreeRetained
	}
	if meta, err := execenv.ReadGCMeta(row.Path); err == nil && meta.LocalDirectory && row.NextAction == protocol.WorktreeCleanup {
		row.NextAction = protocol.WorktreeRetained
	}
}

func summarizeWorktreeActions(details []protocol.WorktreeRepositoryLifecycle) protocol.WorktreeNextAction {
	result := protocol.WorktreeCleanup
	priority := map[protocol.WorktreeNextAction]int{
		protocol.WorktreeCleanup: 0, protocol.WorktreeMerge: 1, protocol.WorktreeRetained: 2,
		protocol.WorktreeReview: 3, protocol.WorktreeChangesRequested: 4, protocol.WorktreeUnknown: 5, protocol.WorktreeActive: 6,
	}
	for _, detail := range details {
		if priority[detail.NextAction] > priority[result] {
			result = detail.NextAction
		}
	}
	return result
}
