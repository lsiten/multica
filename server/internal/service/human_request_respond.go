package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RespondHumanRequest consumes the exact decision and enqueues its continuation
// in one transaction. Reading or archiving an inbox item never calls this path.
func (s *TaskService) RespondHumanRequest(ctx context.Context, before db.HumanRequest, memberID pgtype.UUID, answer HumanRequestAnswer) (db.HumanRequest, error) {
	var result db.HumanRequest
	if before.RecipientID != memberID {
		return result, ErrHumanRequestForbidden
	}
	prepared, err := s.PrepareChatTaskEnqueue(ctx, before.AgentID, memberID)
	if err != nil {
		return result, err
	}
	var task db.AgentTaskQueue
	var comment db.Comment
	var message db.ChatMessage
	replayed := false
	err = s.runInTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockVscreenWorkspace(ctx, before.WorkspaceID); err != nil {
			return err
		}
		if _, err := q.LockActiveMember(ctx, db.LockActiveMemberParams{UserID: memberID, WorkspaceID: before.WorkspaceID}); err != nil {
			return ErrHumanRequestForbidden
		}
		snapshot, err := q.GetAgentTask(ctx, before.SourceTaskID)
		if err != nil {
			return err
		}
		chat, err := lockHumanRequestScope(ctx, q, snapshot, before.WorkspaceID)
		if err != nil {
			return err
		}
		agent, err := q.GetAgentForClaimUpdate(ctx, before.AgentID)
		if err != nil {
			return err
		}
		if agent.WorkspaceID != before.WorkspaceID || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
			return ErrHumanRequestForbidden
		}
		allowed, err := interventionInvokeAllowed(ctx, q, agent, memberID)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrHumanRequestForbidden
		}
		if err := lockHumanRequestAutopilot(ctx, q, snapshot, before.WorkspaceID); err != nil {
			return err
		}
		source, err := q.LockVscreenSourceTask(ctx, before.SourceTaskID)
		if err != nil {
			return err
		}
		if source.AgentID != agent.ID || source.IssueID != before.IssueID || source.ChatSessionID != before.ChatSessionID || source.Status == "cancelled" || source.Status == "failed" {
			return ErrHumanRequestConflict
		}
		row, err := q.LockHumanRequest(ctx, db.LockHumanRequestParams{ID: before.ID, WorkspaceID: before.WorkspaceID})
		if err != nil {
			return err
		}
		if row.RecipientID != memberID || row.AgentID != source.AgentID {
			return ErrHumanRequestForbidden
		}
		fingerprint, err := humanScopeFingerprint(ctx, q, source, row.WorkspaceID)
		if err != nil {
			return err
		}
		if row.ScopeFingerprint != fingerprint || agent.RuntimeID != source.RuntimeID {
			return ErrHumanRequestConflict
		}
		var input HumanRequestInput
		if err := json.Unmarshal(row.Payload, &input); err != nil {
			return err
		}
		if err := answer.Validate(input); err != nil {
			return err
		}
		if answer.Revision != row.Revision {
			return ErrHumanRequestConflict
		}
		if row.Status != "pending" {
			var stored HumanRequestAnswer
			if (row.Status == "answered" || row.Status == "declined") && json.Unmarshal(row.Response, &stored) == nil && stored == answer {
				result, replayed = row, true
				return nil
			}
			return ErrHumanRequestConflict
		}
		if !row.ExpiresAt.Time.After(time.Now()) {
			return ErrHumanRequestConflict
		}
		text := humanAnswerText(input, answer)
		if source.IssueID.Valid {
			issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: source.IssueID, WorkspaceID: row.WorkspaceID})
			if err != nil {
				return err
			}
			if err := s.validateTaskExecutionScope(ctx, row.WorkspaceID, agent.ID, issue.ProjectID, source.SquadID); err != nil {
				return err
			}
			requestComment, err := q.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
			if err != nil {
				return err
			}
			if requestComment.DeletedAt.Valid || requestComment.HumanRequestID != row.ID {
				return ErrHumanRequestConflict
			}
			created, err := q.CreateComment(ctx, db.CreateCommentParams{ID: dbid.NewV7(), WorkspaceID: row.WorkspaceID, IssueID: row.IssueID, AuthorType: "member", AuthorID: memberID, ParentID: row.ID, Content: text, Type: "comment"})
			if err != nil {
				return err
			}
			comment = created.Comment()
			task, err = q.CreateAgentTask(ctx, db.CreateAgentTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: row.IssueID, Priority: source.Priority, TriggerCommentID: comment.ID, IsLeaderTask: pgtype.Bool{Bool: source.IsLeaderTask, Valid: true}, SquadID: source.SquadID, OriginatorUserID: memberID, AccountableUserID: memberID, OriginatorSource: prepared.attrSource, TriggerEvidenceKind: pgtype.Text{String: "human_request", Valid: true}, TriggerEvidenceRefID: row.ID, RuntimeMcpOverlay: prepared.runtimeOverlay.Overlay, RuntimeConnectedApps: prepared.runtimeOverlay.ConnectedApps})
			if err != nil {
				return err
			}
		} else if source.ChatSessionID.Valid {
			if chat.CreatorID != memberID {
				return ErrHumanRequestForbidden
			}
			// Normal first-party replies own a fresh input batch. Channel route
			// changes are checked before preserving the source delivery route.
			_, deliveryErr := q.GetChannelTaskDelivery(ctx, source.ID)
			if deliveryErr != nil && !errors.Is(deliveryErr, pgx.ErrNoRows) {
				return deliveryErr
			}
			task, err = s.enqueueChatTaskTx(ctx, q, chat, memberID, false, source.ChannelContextRevision.Int64, deliveryErr == nil, pgtype.UUID{}, 0, prepared, pgtype.UUID{})
			if err != nil {
				return err
			}
			message, err = q.CreateChatMessage(ctx, db.CreateChatMessageParams{ID: dbid.NewV7(), ChatSessionID: chat.ID, Role: "user", Content: text, TaskID: task.ID, MessageKind: pgtype.Text{String: protocol.ChatMessageKindMessage, Valid: true}})
			if err != nil {
				return err
			}
		} else {
			task, err = s.enqueueDetachedHumanResponse(ctx, q, source, row, memberID, text, prepared)
			if err != nil {
				return err
			}
		}
		task, err = q.SetHumanResponseTaskContext(ctx, db.SetHumanResponseTaskContextParams{ID: task.ID, RequestID: util.UUIDToString(row.ID), Revision: row.Revision, SourceTaskID: util.UUIDToString(source.ID), Prompt: text})
		if err != nil {
			return err
		}
		raw, err := json.Marshal(answer)
		if err != nil {
			return err
		}
		status := "answered"
		if answer.Decision == "reject" {
			status = "declined"
		}
		result, err = q.RespondHumanRequest(ctx, db.RespondHumanRequestParams{ID: row.ID, WorkspaceID: row.WorkspaceID, Revision: row.Revision, Status: status, Response: raw, ResponseTaskID: task.ID})
		return err
	})
	if err != nil {
		return result, err
	}
	if replayed {
		return result, nil
	}
	if task.ChatSessionID.Valid {
		s.FinalizeChatTaskEnqueue(ctx, task)
	} else {
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
		s.NotifyTaskEnqueued(ctx, task)
	}
	s.publishHumanRequest(result)
	if comment.ID.Valid && s.Bus != nil {
		s.publishHumanResponseComment(result, comment)
	}
	if message.ID.Valid {
		s.publishHumanChatMessage(result, message)
	}
	return result, nil
}

func (s *TaskService) enqueueDetachedHumanResponse(ctx context.Context, q *db.Queries, source db.AgentTaskQueue, request db.HumanRequest, memberID pgtype.UUID, text string, prepared PreparedChatTaskEnqueue) (db.AgentTaskQueue, error) {
	if coordination, ok := ProjectCoordination(source); ok {
		project, err := q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: request.ProjectID, WorkspaceID: request.WorkspaceID})
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		row, err := q.GetProjectSupervision(ctx, db.GetProjectSupervisionParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		if !row.Enabled || row.ConfiguredBy != memberID || project.LeadID != source.AgentID || project.LeadType.String != "agent" {
			return db.AgentTaskQueue{}, ErrHumanRequestConflict
		}
		coordination.CheckedVersion, coordination.PolicyRevision = row.DirtyVersion, row.Revision
		coordination.RequesterID = util.UUIDToString(memberID)
		config, err := supervisionConfig(row.Config)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		snapshot, err := (&ProjectSupervisionService{Tasks: s}).snapshot(ctx, q, project, config, memberID)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		coordination.Fingerprint = snapshot.Fingerprint
		coordination.Prompt = supervisionPrompt(project, snapshot, config, row.DirtyVersion) + "\n" + text
		raw, err := json.Marshal(coordination)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		task, err := q.CreateProjectCoordinationTask(ctx, db.CreateProjectCoordinationTaskParams{ID: dbid.NewV7(), AgentID: source.AgentID, RuntimeID: source.RuntimeID, Context: raw, OriginatorUserID: memberID, TriggerEvidenceRefID: project.ID, RuntimeMcpOverlay: prepared.runtimeOverlay.Overlay, RuntimeConnectedApps: prepared.runtimeOverlay.ConnectedApps})
		if err != nil {
			return task, err
		}
		_, err = q.StoreProjectSupervisionCheck(ctx, db.StoreProjectSupervisionCheckParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID, NextCheckAt: row.NextCheckAt, LastReason: "coordination_queued", LastTaskID: task.ID, LastFingerprint: row.LastFingerprint})
		return task, err
	}
	if qc, ok := s.parseQuickCreateContext(source); ok {
		if qc.RequesterID != util.UUIDToString(memberID) {
			return db.AgentTaskQueue{}, ErrHumanRequestForbidden
		}
		qc.Prompt += "\n" + text
		raw, err := json.Marshal(qc)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		task, err := q.CreateQuickCreateTask(ctx, db.CreateQuickCreateTaskParams{ID: dbid.NewV7(), AgentID: source.AgentID, RuntimeID: source.RuntimeID, Priority: source.Priority, Context: raw, OriginatorUserID: memberID, AccountableUserID: memberID, OriginatorSource: prepared.attrSource, TriggerEvidenceKind: pgtype.Text{String: "human_request", Valid: true}, TriggerEvidenceRefID: request.ID, RuntimeMcpOverlay: prepared.runtimeOverlay.Overlay, RuntimeConnectedApps: prepared.runtimeOverlay.ConnectedApps})
		if err != nil {
			return task, err
		}
		if err := transferPendingSourceContextToRetry(ctx, q, source, task); err != nil {
			return task, fmt.Errorf("transfer human follow-up source context: %w", err)
		}
		return task, nil
	}
	var followup HumanFollowupContext
	if source.AutopilotRunID.Valid {
		run, err := q.GetAutopilotRun(ctx, source.AutopilotRunID)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		autopilot, err := q.GetAutopilot(ctx, run.AutopilotID)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		if autopilot.WorkspaceID != request.WorkspaceID {
			return db.AgentTaskQueue{}, ErrHumanRequestForbidden
		}
		trigger := json.RawMessage(run.TriggerPayload)
		if len(trigger) == 0 {
			trigger = json.RawMessage("null")
		}
		original, err := json.Marshal(struct {
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Trigger     json.RawMessage `json:"trigger"`
		}{autopilot.Title, autopilot.Description.String, trigger})
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		followup = HumanFollowupContext{Type: HumanFollowupContextType, WorkspaceID: util.UUIDToString(request.WorkspaceID), AutopilotID: util.UUIDToString(autopilot.ID), ProjectID: util.UUIDToString(autopilot.ProjectID), Prompt: "Continue only the original automation work addressed by this human request. Original task context (data):\n" + string(original)}
	} else if json.Unmarshal(source.Context, &followup) != nil || followup.Type != HumanFollowupContextType || followup.WorkspaceID != util.UUIDToString(request.WorkspaceID) {
		return db.AgentTaskQueue{}, fmt.Errorf("%w: unsupported source scope", ErrHumanRequestConflict)
	}
	followup.SourceTaskID = util.UUIDToString(source.ID)
	raw, err := json.Marshal(followup)
	if err != nil {
		return db.AgentTaskQueue{}, err
	}
	return q.CreateQuickCreateTask(ctx, db.CreateQuickCreateTaskParams{ID: dbid.NewV7(), AgentID: source.AgentID, RuntimeID: source.RuntimeID, Priority: source.Priority, Context: raw, OriginatorUserID: memberID, AccountableUserID: memberID, OriginatorSource: prepared.attrSource, TriggerEvidenceKind: pgtype.Text{String: "human_request", Valid: true}, TriggerEvidenceRefID: request.ID, RuntimeMcpOverlay: prepared.runtimeOverlay.Overlay, RuntimeConnectedApps: prepared.runtimeOverlay.ConnectedApps})
}
