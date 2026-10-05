package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ReportIssueNextStep stores only the authenticated run's current handoff.
func (h *Handler) ReportIssueNextStep(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, 403, "an active issue run is required")
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "task_id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Agent-ID"), "agent_id")
	if !ok {
		return
	}
	var step service.IssueNextStep
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&step); err != nil {
		writeError(w, 400, "invalid next step")
		return
	}
	step.SourceTaskID = uuidToString(taskID)
	if err := step.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if step.IssueRevision != issue.Revision {
		writeError(w, 409, "issue changed; reread before reporting")
		return
	}
	if step.ActorID != "" {
		actorID, ok := parseUUIDOrBadRequest(w, step.ActorID, "actor_id")
		if !ok {
			return
		}
		if step.ActorType == "agent" && actorID != agentID {
			writeError(w, 400, "agent handoffs must name the reporting agent")
			return
		}
		if step.ActorType == "member" {
			if _, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{UserID: actorID, WorkspaceID: issue.WorkspaceID}); err != nil {
				writeError(w, 400, "actor is not a workspace member")
				return
			}
		} else {
			if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: actorID, WorkspaceID: issue.WorkspaceID}); err != nil {
				writeError(w, 400, "actor is not a workspace agent")
				return
			}
		}
	}
	if step.RequestID != "" {
		requestID, ok := parseUUIDOrBadRequest(w, step.RequestID, "request_id")
		if !ok {
			return
		}
		request, err := h.Queries.GetHumanRequest(r.Context(), db.GetHumanRequestParams{ID: requestID, WorkspaceID: issue.WorkspaceID})
		if err != nil || request.SourceTaskID != taskID || request.IssueID != issue.ID || request.Status != "pending" || step.ActorType != "member" || uuidToString(request.RecipientID) != step.ActorID {
			writeError(w, 409, "handoff request does not belong to this run")
			return
		}
	}
	scope, err := h.Queries.GetIssueNextStepScope(r.Context(), db.GetIssueNextStepScopeParams{IssueID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		writeError(w, 500, "cannot read handoff scope")
		return
	}
	step.ScopeFingerprint = scope
	raw, err := json.Marshal(step)
	if err != nil {
		writeError(w, 500, "cannot encode handoff")
		return
	}
	_, err = h.Queries.ReportIssueNextStep(r.Context(), db.ReportIssueNextStepParams{TaskID: taskID, AgentID: agentID, IssueID: issue.ID, IssueRevision: step.IssueRevision, NextStep: raw})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "run changed or ended")
		return
	}
	if err != nil {
		writeError(w, 500, "cannot save handoff")
		return
	}
	h.publish("task:progress", uuidToString(issue.WorkspaceID), "agent", uuidToString(agentID), map[string]string{"issue_id": uuidToString(issue.ID), "task_id": uuidToString(taskID), "kind": "next_step"})
	writeJSON(w, 200, step)
}

// PerformIssueProgressAction revalidates the card evidence before accepting member work.
func (h *Handler) PerformIssueProgressAction(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if r.Header.Get("X-Actor-Source") == "task_token" {
		writeError(w, 403, "a member must choose this operation")
		return
	}
	member, ok := h.workspaceMember(w, r, uuidToString(issue.WorkspaceID))
	if !ok {
		return
	}
	var input service.ProgressActionInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		writeError(w, 400, "invalid progress action")
		return
	}
	if input.Kind == "review" && input.Verdict == "accept" {
		accepted, err := h.TaskService.AcceptProgressReview(r.Context(), issue, member.UserID, input)
		if errors.Is(err, service.ErrProgressSnapshotChanged) {
			writeJSON(w, 409, map[string]string{"code": "progress_snapshot_changed", "error": "delivery changed; refresh before accepting"})
			return
		}
		if err != nil {
			humanRequestError(w, err)
			return
		}
		response := issueToResponse(accepted, h.getIssuePrefix(r.Context(), accepted.WorkspaceID))
		h.fillStatusCategory(r.Context(), accepted.WorkspaceID, &response)
		h.publish("issue:updated", uuidToString(accepted.WorkspaceID), "member", uuidToString(member.UserID), map[string]any{"issue": response, "status_changed": true, "prev_status": issue.Status})
		h.processChildEvents(r.Context(), accepted.ParentIssueID, issue.ParentIssueID)
		writeJSON(w, 200, map[string]any{"issue_id": uuidToString(accepted.ID), "status": accepted.Status, "task_id": nil})
		return
	}
	task, err := h.TaskService.PerformProgressAction(r.Context(), issue, member.UserID, input)
	if errors.Is(err, service.ErrProgressSnapshotChanged) {
		writeJSON(w, 409, map[string]string{"code": "progress_snapshot_changed", "error": "work changed; refresh the card"})
		return
	}
	if err != nil {
		humanRequestError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"task_id": uuidToString(task.ID), "status": task.Status, "issue_id": uuidToString(issue.ID)})
}
