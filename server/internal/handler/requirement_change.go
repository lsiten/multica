package handler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Requirement change-impact actions the lead decides per sub-task when a
// confirmed requirement changes. The lead's decision is authoritative: it is
// applied to the sub-task (not merely advisory).
const (
	ChangeImpactKeep   = "keep"   // the change does not affect the sub-task
	ChangeImpactRedo   = "redo"   // rework: reopen to todo, abort active work
	ChangeImpactCancel = "cancel" // the sub-task is no longer needed
	ChangeImpactAdjust = "adjust" // keep status, flag for re-review
)

// changeReviewDecision is one lead decision for one sub-issue.
type changeReviewDecision struct {
	SubIssueID pgtype.UUID `json:"sub_issue_id"`
	Action      string      `json:"action"`
}

// applyRequirementChangeReview applies the lead's change-impact decisions to the
// requirement's sub-issues. Each action is authoritative:
//
//	keep   — no change.
//	redo   — reopen the sub-issue to todo and abort its active work (covers
//	         "all sub-task states" incl. done via reopen).
//	cancel — set the sub-issue to cancelled and abort its active work.
//	adjust — keep the status, record change_impact so the worker re-reviews.
//
// A decision for an unknown sub-issue is skipped. Metadata failures are
// non-fatal (the status flip is the important part); a status flip failure
// returns an error so the caller can retry the whole review.
func (h *Handler) applyRequirementChangeReview(ctx context.Context, reqIssue db.Issue, decisions []changeReviewDecision) error {
	byID := map[pgtype.UUID]changeReviewDecision{}
	for _, d := range decisions {
		byID[d.SubIssueID] = d
	}
	for _, sub := range h.listRequirementSubIssuesForChange(ctx, reqIssue.WorkspaceID, reqIssue.ID) {
		d, ok := byID[sub.ID]
		if !ok {
			continue
		}
		if err := h.applyOneChangeDecision(ctx, sub, d); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) applyOneChangeDecision(ctx context.Context, sub db.Issue, d changeReviewDecision) error {
	switch d.Action {
	case ChangeImpactKeep:
		return nil
	case ChangeImpactCancel:
		if err := h.setSubIssueStatus(ctx, sub, issuestatus.Cancelled); err != nil {
			return err
		}
		h.abortActiveWorkOnIssue(ctx, sub)
		h.setChangeImpact(ctx, sub, ChangeImpactCancel)
		return nil
	case ChangeImpactAdjust:
		h.setChangeImpact(ctx, sub, ChangeImpactAdjust)
		return nil
	case ChangeImpactRedo:
		// Reopen to todo covers a done/in_review sub-issue; abort active work.
		if err := h.setSubIssueStatus(ctx, sub, issuestatus.Todo); err != nil {
			return err
		}
		h.abortActiveWorkOnIssue(ctx, sub)
		h.setChangeImpact(ctx, sub, ChangeImpactRedo)
		return nil
	default:
		return nil
	}
}

func (h *Handler) setSubIssueStatus(ctx context.Context, sub db.Issue, status string) error {
	if _, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID:          sub.ID,
		Status:      status,
		WorkspaceID: sub.WorkspaceID,
	}); err != nil {
		return err
	}
	return nil
}

// abortActiveWorkOnIssue cancels every active task on the sub-issue so a redo
// or cancel does not leave development running against a stale requirement.
func (h *Handler) abortActiveWorkOnIssue(ctx context.Context, sub db.Issue) {
	_, _ = h.Queries.CancelAgentTasksByIssue(ctx, sub.ID)
}

// setChangeImpact records the change-impact marker on the sub-issue's metadata.
// Best-effort: a metadata write failure must not undo the status flip.
func (h *Handler) setChangeImpact(ctx context.Context, sub db.Issue, action string) {
	value, _ := json.Marshal(action)
	_, _ = h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		Key:         "change_impact",
		Value:       value,
		ID:          sub.ID,
		WorkspaceID: sub.WorkspaceID,
	})
}

// listRequirementSubIssuesForChange is a thin wrapper so the change-review
// application can list sub-issues without the handler duplicating the query
// name; it mirrors ListRequirementSubIssues.
func (h *Handler) listRequirementSubIssuesForChange(ctx context.Context, workspaceID, requirementIssueID pgtype.UUID) []db.Issue {
	rows, err := h.Queries.ListRequirementSubIssues(ctx, db.ListRequirementSubIssuesParams{
		RequirementIssueID: requirementIssueID,
		WorkspaceID:        workspaceID,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
	}
	return rows
}
