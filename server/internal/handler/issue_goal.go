package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type issueGoalResponse struct {
	Objective       string  `json:"objective"`
	CompletedAt     *string `json:"completed_at"`
	CompletedByType *string `json:"completed_by_type"`
	CompletedByID   *string `json:"completed_by_id"`
}

func issueGoalToResponse(goal db.IssueGoal) issueGoalResponse {
	return issueGoalResponse{
		Objective:       goal.Objective,
		CompletedAt:     timestampToStringPtr(goal.CompletedAt),
		CompletedByType: textToPtr(goal.CompletedByType),
		CompletedByID:   uuidToPtr(goal.CompletedByID),
	}
}

func timestampToStringPtr(value pgtype.Timestamptz) *string {
	if !value.Valid {
		return nil
	}
	formatted := timestampToString(value)
	return &formatted
}

type upsertIssueGoalRequest struct {
	Objective string `json:"objective"`
}

type issueGoalActor struct {
	Type string
	ID   pgtype.UUID
}

func (h *Handler) GetIssueGoal(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	goal, err := h.Queries.GetIssueGoal(r.Context(), issue.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "goal mode is not enabled")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load goal")
		return
	}
	writeJSON(w, http.StatusOK, issueGoalToResponse(goal))
}

func (h *Handler) UpsertIssueGoal(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req upsertIssueGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	objective := strings.TrimSpace(req.Objective)
	if objective == "" {
		writeError(w, http.StatusBadRequest, "objective is required")
		return
	}
	goal, err := h.Queries.UpsertIssueGoal(r.Context(), db.UpsertIssueGoalParams{
		IssueID:   issue.ID,
		Objective: objective,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "cannot update the goal while this issue is in review")
		return
	}
	writeJSON(w, http.StatusOK, issueGoalToResponse(goal))
}

func (h *Handler) DeleteIssueGoal(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := h.Queries.DeleteIssueGoal(r.Context(), issue.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disable goal mode")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CompleteIssueGoal(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(issue.WorkspaceID))
	actorUUID, err := util.ParseUUID(actorID)
	actor := issueGoalActor{Type: actorType, ID: actorUUID}
	if err != nil || !h.isIssueGoalOwner(r, issue, actor) {
		writeError(w, http.StatusForbidden, "only the current assignee can complete this goal")
		return
	}
	goal, err := h.Queries.CompleteIssueGoal(r.Context(), db.CompleteIssueGoalParams{
		IssueID:         issue.ID,
		CompletedByType: pgtype.Text{String: actor.Type, Valid: true},
		CompletedByID:   actor.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "goal mode is not enabled or is already completed")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete goal")
		return
	}
	writeJSON(w, http.StatusOK, issueGoalToResponse(goal))
}

func (h *Handler) isIssueGoalOwner(r *http.Request, issue db.Issue, actor issueGoalActor) bool {
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid || !actor.ID.Valid {
		return false
	}
	switch issue.AssigneeType.String {
	case "member", "agent":
		return issue.AssigneeType.String == actor.Type && issue.AssigneeID == actor.ID
	case "squad":
		if actor.Type != "agent" {
			return false
		}
		squad, err := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{
			ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID,
		})
		return err == nil && squad.LeaderID == actor.ID
	default:
		return false
	}
}

func (h *Handler) requireCompletedIssueGoalForReview(r *http.Request, issueID pgtype.UUID) bool {
	goal, err := h.Queries.GetIssueGoal(r.Context(), issueID)
	return errors.Is(err, pgx.ErrNoRows) || (err == nil && goal.CompletedAt.Valid)
}
