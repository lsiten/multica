package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RequirementResponse is the JSON for a requirement lifecycle (1:1 with a
// kind='requirement' issue). The requirement document is the backing issue's
// description; its revisions live in document_content_version.
type RequirementResponse struct {
	IssueID           string  `json:"issue_id"`
	LifecycleStatus   string  `json:"lifecycle_status"`
	ConfirmedRevision int64   `json:"confirmed_revision,omitempty"`
	ConfirmedBy       *string `json:"confirmed_by,omitempty"`
	ConfirmedAt       *string `json:"confirmed_at,omitempty"`
	UnconfirmedChange bool    `json:"unconfirmed_change"`
}

func requirementToResponse(r db.Requirement) RequirementResponse {
	resp := RequirementResponse{
		IssueID:           uuidToString(r.IssueID),
		LifecycleStatus:   r.LifecycleStatus,
		UnconfirmedChange: r.UnconfirmedChange,
	}
	if r.ConfirmedRevision.Valid {
		resp.ConfirmedRevision = r.ConfirmedRevision.Int64
	}
	if r.ConfirmedBy.Valid {
		s := uuidToString(r.ConfirmedBy)
		resp.ConfirmedBy = &s
	}
	if r.ConfirmedAt.Valid {
		t := r.ConfirmedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		resp.ConfirmedAt = &t
	}
	return resp
}

// GetRequirement returns the lifecycle for a kind='requirement' issue, or 404
// when the issue is not a requirement.
func (h *Handler) GetRequirement(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	if issue.Kind != "requirement" {
		writeError(w, http.StatusNotFound, "not a requirement")
		return
	}
	req, err := h.Queries.GetRequirementByIssue(r.Context(), db.GetRequirementByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "requirement not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load requirement")
		return
	}
	writeJSON(w, http.StatusOK, requirementToResponse(req))
}

// confirmRequirement pins the current document revision and records the
// confirmer. Any workspace member may confirm; the confirmed actor is stored.
// It is called by the agent run resumed by the human's confirmation, not a
// direct human endpoint (the human only answers the card).
func (h *Handler) confirmRequirement(ctx context.Context, issueID, workspaceID, confirmedBy pgtype.UUID, revision int64) (db.Requirement, error) {
	return h.Queries.ConfirmRequirement(ctx, db.ConfirmRequirementParams{
		ConfirmedRevision: pgtype.Int8{Int64: revision, Valid: true},
		ConfirmedBy:       confirmedBy,
		IssueID:         issueID,
		WorkspaceID:     workspaceID,
	})
}

// maybeReopenRequirementOnEdit flips a confirmed requirement back to
// pending_confirm when its document is edited after confirmation. The edit IS
// the change; the agent then initiates the change confirmation.
func (h *Handler) maybeReopenRequirementOnEdit(ctx context.Context, issue db.Issue) {
	if issue.Kind != "requirement" {
		return
	}
	// ReopenRequirementForChange only affects a currently confirmed requirement
	// (guarded by lifecycle_status = 'confirmed'); other rows are no-ops.
	if _, err := h.Queries.ReopenRequirementForChange(ctx, db.ReopenRequirementForChangeParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		// Best-effort: the document edit already committed; a reopen failure must
		// not fail the edit.
		return
	}
}

// request_key values bind an agent-initiated human_request to a deterministic
// requirement-lifecycle transition. The agent creates the card through the
// existing human_request flow; the member only answers. The answer drives the
// lifecycle server-side so the transition does not depend on agent LLM
// behaviour (the human "just confirms").
const (
	requirementConfirmKey      = "requirement-confirm"
	requirementChangeReviewKey = "requirement-change-review"
)

// applyRequirementFromHumanResponse deterministically applies a requirement
// lifecycle transition once the member has answered the agent's card. It is
// best-effort: the answer is already recorded and the agent's continuation task
// still runs, so a failure here must not fail the member's response.
func (h *Handler) applyRequirementFromHumanResponse(ctx context.Context, req db.HumanRequest, memberID pgtype.UUID) {
	if req.IssueID == (pgtype.UUID{}) {
		return
	}
	var input service.HumanRequestInput
	if err := json.Unmarshal(req.Payload, &input); err != nil {
		return
	}
	var answer service.HumanRequestAnswer
	if err := json.Unmarshal(req.Response, &answer); err != nil {
		return
	}
	switch req.RequestKey {
	case requirementConfirmKey:
		if answer.Decision == "reject" {
			return
		}
		h.confirmRequirementFromAnswer(ctx, req, memberID)
	case requirementChangeReviewKey:
		if answer.Decision == "reject" {
			return
		}
		h.applyChangeReviewFromAnswer(ctx, req, answer)
	}
}

// confirmRequirementFromAnswer pins the current document revision as confirmed.
// The confirmed actor is the member who answered.
func (h *Handler) confirmRequirementFromAnswer(ctx context.Context, req db.HumanRequest, memberID pgtype.UUID) {
	revision, err := h.currentDocumentRevision(ctx, req.IssueID, req.WorkspaceID)
	if err != nil {
		return
	}
	if _, err := h.confirmRequirement(ctx, req.IssueID, req.WorkspaceID, memberID, revision); err != nil {
		// Best-effort: the answer is recorded; the agent's continuation re-drives.
	}
}

// applyChangeReviewFromAnswer applies the lead's change-impact decision to the
// requirement's sub-issues and re-confirms the requirement at the (edited)
// current revision, so the change becomes the new confirmed baseline.
func (h *Handler) applyChangeReviewFromAnswer(ctx context.Context, req db.HumanRequest, answer service.HumanRequestAnswer) {
	action, ok := changeReviewActionForAnswer(answer)
	if !ok {
		return
	}
	reqIssue := db.Issue{ID: req.IssueID, WorkspaceID: req.WorkspaceID}
	decisions := h.changeReviewDecisionsForSubIssues(ctx, req.WorkspaceID, req.IssueID, action)
	if err := h.applyRequirementChangeReview(ctx, reqIssue, decisions); err != nil {
		return
	}
	revision, err := h.currentDocumentRevision(ctx, req.IssueID, req.WorkspaceID)
	if err != nil {
		return
	}
	if _, err := h.confirmRequirement(ctx, req.IssueID, req.WorkspaceID, req.RecipientID, revision); err != nil {
		// Best-effort: the sub-issue impact is the important part.
	}
}

// changeReviewDecisionsForSubIssues maps a single global change-impact action
// onto every sub-issue of the requirement, so one lead decision covers all
// sub-task states and development situations.
func (h *Handler) changeReviewDecisionsForSubIssues(ctx context.Context, workspaceID, requirementIssueID pgtype.UUID, action string) []changeReviewDecision {
	var decisions []changeReviewDecision
	for _, sub := range h.listRequirementSubIssuesForChange(ctx, workspaceID, requirementIssueID) {
		decisions = append(decisions, changeReviewDecision{SubIssueID: sub.ID, Action: action})
	}
	return decisions
}

// changeReviewActionForAnswer maps a card answer's chosen choice to a
// change-impact action. The agent authors the change-review card with choice
// IDs equal to the action semantics, so the answer's chosen ID is the action.
func changeReviewActionForAnswer(answer service.HumanRequestAnswer) (string, bool) {
	action := strings.ToLower(strings.TrimSpace(answer.Answer))
	switch action {
	case ChangeImpactKeep, ChangeImpactRedo, ChangeImpactCancel, ChangeImpactAdjust:
		return action, true
	}
	return "", false
}

// currentDocumentRevision returns the latest committed content revision for the
// requirement's issue body, defaulting to 1 when no version row exists.
func (h *Handler) currentDocumentRevision(ctx context.Context, issueID, workspaceID pgtype.UUID) (int64, error) {
	latest, err := h.Queries.GetLatestDocumentContentVersion(ctx, db.GetLatestDocumentContentVersionParams{
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issueID,
		WorkspaceID: workspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	return latest.Revision, nil
}

