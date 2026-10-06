package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/util"
)

type environmentReviewReferencesKey struct{}
type environmentReviewReference struct {
	taskID  string
	capture bool
}

// Review receipts can belong to a per-run directory referencing shared code.
// Pin the physical source, rather than keeping every historical run directory.
func (d *Daemon) withEnvironmentReviewReferences(ctx context.Context, roots []string) context.Context {
	references := map[string]environmentReviewReference{}
	for _, root := range roots {
		if ctx.Err() != nil {
			break
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		hasReview := false
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".local-review-") && strings.HasSuffix(entry.Name(), ".json") {
				hasReview = true
				break
			}
		}
		capturing, captureErr := localreview.HasActiveCapture(ctx, root, time.Now())
		if !hasReview && !capturing {
			continue
		}
		owner, err := d.gcTaskDirOwner(root)
		if err != nil {
			continue
		}
		status, err := d.environmentTaskGCStatus(ctx, root, owner, nil)
		if err != nil || !status.RetentionSupported || status.Missing {
			continue
		}
		if !capturing && status.IssueID != "" {
			if status.IssueStatusCategory != issuestatus.CategoryStarted {
				continue
			}
		} else if !capturing && (status.ChatSessionID == "" || status.ChatStatus != "active") {
			continue
		}
		source := root
		if binding, err := execenv.ReadReviewDirectory(root); err == nil && binding.WorkspaceID == owner.WorkspaceID && binding.TaskID == owner.TaskID {
			if codeRoot := managedCodeRoot(d.cfg.WorkspacesRoot, binding.Path); codeRoot != "" {
				canonicalRoot, err := util.ResolveSymlinks(d.cfg.WorkspacesRoot)
				if err != nil {
					continue
				}
				relative, err := filepath.Rel(canonicalRoot, codeRoot)
				if err != nil || !filepath.IsLocal(relative) {
					continue
				}
				source = filepath.Join(d.cfg.WorkspacesRoot, relative)
			}
		}
		sourceOwner, err := d.gcTaskDirOwner(source)
		if err != nil || sourceOwner.WorkspaceID != owner.WorkspaceID {
			continue
		}
		if capturing || captureErr != nil {
			references[executionEnvClaimKey(source)] = environmentReviewReference{taskID: owner.TaskID, capture: true}
			references[executionEnvClaimKey(root)] = environmentReviewReference{taskID: owner.TaskID, capture: true}
			continue
		}
		repositories, _ := inspectWorktreeRepositories(ctx, source, d.cfg.WorkspacesRoot)
		for _, repository := range repositories {
			decision, err := localreview.InspectDecision(ctx, root, repository)
			if err != nil {
				continue
			}
			if (decision.ReviewState == "open" || decision.ReviewState == "approved" || decision.ReviewState == "changes_requested") && (decision.Reason == "review" || decision.Reason == "approved" || decision.Reason == "changes_requested" || decision.Reason == "dirty") {
				references[executionEnvClaimKey(source)] = environmentReviewReference{taskID: owner.TaskID}
				references[executionEnvClaimKey(root)] = environmentReviewReference{taskID: owner.TaskID}
			}
		}
	}
	return context.WithValue(ctx, environmentReviewReferencesKey{}, references)
}
