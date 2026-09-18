package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type interventionEnqueue struct {
	Source   db.AgentTaskQueue
	Chat     db.ChatSession
	Prepared PreparedChatTaskEnqueue
	Request  InterventionContinuation
}

func (s *TaskService) enqueueIntervention(ctx context.Context, q *db.Queries, in interventionEnqueue) (db.AgentTaskQueue, error) {
	src, req := in.Source, in.Request
	var task db.AgentTaskQueue
	var err error
	if src.ChatSessionID.Valid {
		_, deliveryErr := q.GetChannelTaskDelivery(ctx, src.ID)
		if deliveryErr != nil && !errors.Is(deliveryErr, pgx.ErrNoRows) {
			return task, deliveryErr
		}
		if deliveryErr == nil {
			binding, err := q.LockChannelChatSessionBindingForContext(ctx, src.ChatSessionID)
			if err != nil {
				return task, err
			}
			generation, err := q.LockChannelChatContextGenerationByRevision(ctx, db.LockChannelChatContextGenerationByRevisionParams{ChatSessionID: src.ChatSessionID, Revision: src.ChannelContextRevision.Int64})
			if err != nil {
				return task, err
			}
			if generation.PendingFresh {
				return task, InterventionError("source_inactive")
			}
			if binding.RetiredAt.Valid || binding.ContextRevision != src.ChannelContextRevision.Int64 {
				return task, InterventionError("source_inactive")
			}
		}
		task, err = s.enqueueChatTaskTx(ctx, q, in.Chat, req.ActorID, true, src.ChannelContextRevision.Int64, deliveryErr == nil, pgtype.UUID{}, 0, in.Prepared, src.ID)
	} else if !src.IssueID.Valid {
		task, err = s.enqueueQuickCreateIntervention(ctx, q, in)
	} else {
		task, err = q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
			ID: dbid.NewV7(), AgentID: src.AgentID, RuntimeID: src.RuntimeID, IssueID: src.IssueID, Priority: src.Priority,
			TriggerCommentID: src.TriggerCommentID, CoalescedCommentIds: src.CoalescedCommentIds, TriggerSummary: src.TriggerSummary,
			ForceFreshSession: pgtype.Bool{Bool: true, Valid: true}, IsLeaderTask: pgtype.Bool{Bool: src.IsLeaderTask, Valid: true}, SquadID: src.SquadID,
			HandoffNote: pgtype.Text{String: req.HumanSummary, Valid: req.HumanSummary != ""}, RerunOfTaskID: src.ID,
			OriginatorUserID: req.ActorID, AccountableUserID: in.Prepared.accountableUser,
			RuntimeMcpOverlay: in.Prepared.runtimeOverlay.Overlay, RuntimeConnectedApps: in.Prepared.runtimeOverlay.ConnectedApps,
			OriginatorSource: in.Prepared.attrSource, TriggerEvidenceKind: pgtype.Text{String: "issue", Valid: true}, TriggerEvidenceRefID: src.IssueID,
		})
	}
	if err != nil {
		return task, err
	}
	inputOwner := src.ChatInputTaskID
	if src.ChatSessionID.Valid && !inputOwner.Valid {
		inputOwner = src.ID
	}
	return q.SetVscreenContinuationContext(ctx, db.SetVscreenContinuationContextParams{ID: task.ID, RerunOfTaskID: src.ID, Column3: util.UUIDToString(req.Intervention.ID), Column4: req.HumanSummary, Column5: req.FreshSession, ChatInputTaskID: inputOwner})
}
