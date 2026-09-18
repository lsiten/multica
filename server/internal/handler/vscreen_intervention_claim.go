package handler

import (
	"context"
	"encoding/json"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// applyInterventionClaim overwrites all legacy history guesses with the single persisted source.
func (h *Handler) applyInterventionClaim(ctx context.Context, task db.AgentTaskQueue, resp *AgentTaskResponse) error {
	if !task.RerunOfTaskID.Valid {
		return nil
	}
	var contextData struct {
		Intervention *struct {
			ID    string `json:"intervention_id"`
			Fresh bool   `json:"fresh_session"`
		} `json:"vscreen_intervention"`
	}
	if err := json.Unmarshal(task.Context, &contextData); err != nil || contextData.Intervention == nil {
		return nil
	}
	row, err := h.Queries.GetVscreenInterventionByContinuation(ctx, db.GetVscreenInterventionByContinuationParams{ContinuationTaskID: task.ID, WorkspaceID: parseUUID(resp.WorkspaceID)})
	if err != nil {
		return err
	}
	if row.State != "continued" || row.SourceTaskID != task.RerunOfTaskID || row.AgentID != task.AgentID || row.RuntimeID != task.RuntimeID || uuidToString(row.ID) != contextData.Intervention.ID {
		return service.InterventionError("source_mismatch")
	}
	source, err := h.Queries.GetAgentTask(ctx, row.SourceTaskID)
	if err != nil {
		return err
	}
	matches := rerunSourceMatchesTaskScope(task, source)
	if !task.IssueID.Valid && !task.ChatSessionID.Valid && !task.AutopilotRunID.Valid {
		var currentQC, sourceQC service.QuickCreateContext
		matches = json.Unmarshal(task.Context, &currentQC) == nil && json.Unmarshal(source.Context, &sourceQC) == nil && currentQC.Type == service.QuickCreateContextType && sourceQC.Type == currentQC.Type && currentQC.WorkspaceID == sourceQC.WorkspaceID && currentQC.RequesterID == sourceQC.RequesterID && currentQC.SourceContextID == sourceQC.SourceContextID && task.AgentID == source.AgentID
	}
	if !matches || source.RuntimeID != task.RuntimeID {
		return service.InterventionError("source_mismatch")
	}
	resp.PriorSessionID = ""
	resp.PriorWorkDir = source.WorkDir.String
	resp.PriorSessionResumeUnavailable = false
	if !contextData.Intervention.Fresh {
		if source.SessionRolloutMissing || !source.SessionID.Valid || source.SessionID.String == "" || source.WorkDir.String == "" || service.ResumeUnsafeFailure(source.FailureReason.String, source.Error.String) {
			return service.InterventionError("resume_unavailable")
		}
		resp.PriorSessionID = source.SessionID.String
	}
	resp.HandoffNote = row.HumanSummary
	resp.VscreenIntervention = &protocol.VscreenContinuationContext{InterventionID: uuidToString(row.ID), SourceTaskID: uuidToString(row.SourceTaskID), HumanSummary: row.HumanSummary, FreshSession: contextData.Intervention.Fresh, Epoch: protocol.VscreenEpoch{NativeEpoch: row.NativeEpoch, DisplayGeneration: row.DisplayGeneration, GeometryRevision: uint64(row.GeometryRevision)}, ReturnReceiptID: row.ReturnReceiptID}
	return nil
}

// applyExactChatRerun never uses another chat turn's session for an explicit source.
func (h *Handler) applyExactChatRerun(ctx context.Context, task db.AgentTaskQueue, resp *AgentTaskResponse) error {
	source, err := h.Queries.GetAgentTask(ctx, task.RerunOfTaskID)
	if err != nil {
		return err
	}
	resp.PriorSessionID = ""
	resp.PriorWorkDir = ""
	if !rerunSourceMatchesTaskScope(task, source) || source.ChannelContextRevision != task.ChannelContextRevision {
		return service.InterventionError("source_mismatch")
	}
	resp.PriorWorkDir = source.WorkDir.String
	if source.RuntimeID == task.RuntimeID && !service.ResumeUnsafeFailure(source.FailureReason.String, source.Error.String) && !source.SessionRolloutMissing {
		resp.PriorSessionID = source.SessionID.String
	}
	resp.PriorSessionResumeUnavailable = source.SessionRolloutMissing
	return nil
}
