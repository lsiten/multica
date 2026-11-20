package handler

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	generated "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) WatchAgentRun(w http.ResponseWriter, r *http.Request) {
	defer collaborationTrace(r.Context(), "watch")()
	task, ok := h.loadTaskForUser(w, r)
	if !ok {
		return
	}
	var req agentCollaborationCommand
	if !decodeAgentCollaborationCommand(w, r, &req) {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace id")
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(workspaceID))
	actorUUID, ok := parseUUIDOrBadRequest(w, actorID, "actor_id")
	if !ok {
		return
	}
	if actorType == "agent" && !h.taskSourceOrEmpty(r, actorType, actorID).Valid {
		writeError(w, http.StatusForbidden, "an active source Run is required")
		return
	}
	fp := collaborationFingerprint("watch", req.ClientRequestID, uuidToString(task.ID), actorType, actorID)
	reservation := task
	reservation.ID = h.taskSourceOrEmpty(r, actorType, actorID)
	op, replay, err := h.reserveCollaborationOperation(r.Context(), workspaceID, actorType, actorUUID, req.ClientRequestID, "watch", fp, reservation, task.AgentID)
	if err != nil {
		h.writeCollaborationError(w, err)
		return
	}
	if !replay {
		if op, err = h.Queries.SetAgentCollaborationOperationTargets(r.Context(), generated.SetAgentCollaborationOperationTargetsParams{ID: op.ID, IssueID: task.IssueID, TargetAgentID: task.AgentID, TargetTaskID: task.ID}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save watch receipt")
			return
		}
		if !task.IssueID.Valid {
			writeError(w, http.StatusConflict, "target run has no issue context")
			return
		}
		originator := h.collaborationOriginator(r, actorType, actorID)
		if originator == "" {
			h.failCollaborationOperation(r.Context(), op.ID, "a human originator is required")
			writeError(w, http.StatusForbidden, "a human originator is required")
			return
		}
		wakeupAgentID := task.AgentID
		if source := h.taskSourceOrEmpty(r, actorType, actorID); source.Valid {
			if sourceTask, sourceOK := h.taskFromRequestHeader(r); sourceOK {
				wakeupAgentID = sourceTask.AgentID
			}
		}
		wakeup := service.WakeupInput{AgentID: uuidToString(wakeupAgentID), Instruction: "Continue after the watched Agent run reaches a terminal state.", Kind: "event", Mode: "once", EventTypes: []string{"task.completed", "task.failed", "task.cancelled"}, FilterTaskID: uuidToString(task.ID)}
		wake, saveErr := (&service.IssueWakeupService{Tasks: h.TaskService}).Save(r.Context(), task.IssueID, parseUUID(originator), h.taskSourceOrEmpty(r, actorType, actorID), pgtype.UUID{}, wakeup)
		if saveErr != nil {
			h.failCollaborationOperation(r.Context(), op.ID, saveErr.Error())
			h.writeCollaborationError(w, saveErr)
			return
		}
		result, _ := json.Marshal(map[string]any{"wakeup_id": uuidToString(wake.ID), "source_task_id": uuidToString(task.ID)})
		if _, err = h.Queries.CompleteAgentCollaborationOperation(r.Context(), generated.CompleteAgentCollaborationOperationParams{ID: op.ID, Status: "watching", Result: result}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save watch receipt")
			return
		}
		op.Status = "watching"
		op.Result = result
	}
	writeJSON(w, http.StatusAccepted, collaborationOperationResponse(op, task, generated.CreateCommentRow{}))
}
