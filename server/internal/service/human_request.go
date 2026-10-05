package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// CreateHumanRequest atomically delivers one versioned request for a trusted run.
func (s *TaskService) CreateHumanRequest(ctx context.Context, source db.AgentTaskQueue, workspaceID pgtype.UUID, input HumanRequestInput) (db.HumanRequest, error) {
	var result db.HumanRequest
	if err := input.Validate(); err != nil {
		return result, err
	}
	recipient := source.OriginatorUserID
	if !recipient.Valid {
		recipient = source.AccountableUserID
	}
	if !recipient.Valid {
		return result, ErrHumanRequestForbidden
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	var comment db.Comment
	var message db.ChatMessage
	var inbox db.InboxItem
	changed := false
	commentEvent := protocol.EventCommentCreated
	err = s.runInTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockVscreenWorkspace(ctx, workspaceID); err != nil {
			return err
		}
		if _, err := q.LockActiveMember(ctx, db.LockActiveMemberParams{UserID: recipient, WorkspaceID: workspaceID}); err != nil {
			return ErrHumanRequestForbidden
		}
		if _, err := lockHumanRequestScope(ctx, q, source, workspaceID); err != nil {
			return err
		}
		agent, err := q.GetAgentForClaimUpdate(ctx, source.AgentID)
		if err != nil {
			return err
		}
		if agent.WorkspaceID != workspaceID || agent.ArchivedAt.Valid {
			return ErrHumanRequestForbidden
		}
		if err := lockHumanRequestAutopilot(ctx, q, source, workspaceID); err != nil {
			return err
		}
		current, err := q.LockVscreenSourceTask(ctx, source.ID)
		if err != nil {
			return err
		}
		if current.AgentID != source.AgentID || current.Status != "running" || current.OriginatorUserID != source.OriginatorUserID || current.AccountableUserID != source.AccountableUserID {
			return ErrHumanRequestConflict
		}
		expiry := input.ExpiresInSeconds
		if expiry == 0 {
			expiry = 7 * 86400
		}
		expiresAt := pgtype.Timestamptz{Time: time.Now().Add(time.Duration(expiry) * time.Second), Valid: true}
		fingerprint, err := humanScopeFingerprint(ctx, q, current, workspaceID)
		if err != nil {
			return err
		}
		prior, err := q.GetHumanRequestByKey(ctx, db.GetHumanRequestByKeyParams{WorkspaceID: workspaceID, SourceTaskID: source.ID, RequestKey: input.Key})
		if err == nil {
			if (bytes.Equal(prior.Payload, payload) || humanPayloadEqual(prior.Payload, input)) && prior.ScopeFingerprint == fingerprint {
				result = prior
				return nil
			}
			if prior.Status != "pending" || !prior.ExpiresAt.Time.After(time.Now()) {
				return ErrHumanRequestConflict
			}
			result, err = q.ReplaceHumanRequest(ctx, db.ReplaceHumanRequestParams{ID: prior.ID, WorkspaceID: workspaceID, Payload: payload, ExpiresAt: expiresAt, ScopeFingerprint: fingerprint})
			if err != nil {
				return err
			}
			if source.IssueID.Valid {
				commentEvent = protocol.EventCommentUpdated
				_, err = q.UpdateHumanRequestComment(ctx, db.UpdateHumanRequestCommentParams{RequestID: result.ID, WorkspaceID: workspaceID, Content: humanRequestText(input)})
				if err == nil {
					comment, err = q.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{ID: result.ID, WorkspaceID: workspaceID})
				}
			} else if source.ChatSessionID.Valid {
				err = q.UpdateHumanRequestMessage(ctx, db.UpdateHumanRequestMessageParams{RequestID: result.ID, ChatSessionID: source.ChatSessionID, Content: humanRequestText(input)})
			}
			if err != nil {
				return err
			}
			inbox, err = q.UpdateHumanRequestInbox(ctx, db.UpdateHumanRequestInboxParams{ID: result.ID, WorkspaceID: workspaceID, Title: input.Title, Body: pgtype.Text{String: strings.TrimPrefix(humanRequestText(input), input.Title+"\n"), Valid: true}})
			if err != nil {
				return err
			}
			if err := q.RequeueHumanRequestNotificationDeliveries(ctx, inbox.ID); err != nil {
				return err
			}
			changed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var projectID pgtype.UUID
		if coordination, ok := ProjectCoordination(source); ok {
			projectID, err = util.ParseUUID(coordination.ProjectID)
			if err != nil {
				return ErrHumanRequestInput
			}
		}
		result, err = q.CreateHumanRequest(ctx, db.CreateHumanRequestParams{ID: dbid.NewV7(), WorkspaceID: workspaceID, SourceTaskID: source.ID, AgentID: source.AgentID, RecipientID: recipient, IssueID: source.IssueID, ChatSessionID: source.ChatSessionID, ProjectID: projectID, RequestKey: input.Key, Payload: payload, ExpiresAt: expiresAt, ScopeFingerprint: fingerprint})
		if err != nil {
			return err
		}
		if source.IssueID.Valid {
			created, err := q.CreateComment(ctx, db.CreateCommentParams{ID: result.ID, IssueID: source.IssueID, WorkspaceID: workspaceID, AuthorType: "agent", AuthorID: source.AgentID, Content: humanRequestText(input), Type: "comment", ParentID: source.TriggerCommentID, SourceTaskID: source.ID})
			if err != nil {
				return err
			}
			if err = q.SetCommentHumanRequest(ctx, db.SetCommentHumanRequestParams{RequestID: result.ID, WorkspaceID: workspaceID}); err != nil {
				return err
			}
			comment = created.Comment()
			comment.HumanRequestID = result.ID
		} else if source.ChatSessionID.Valid {
			message, err = q.CreateChatMessage(ctx, db.CreateChatMessageParams{ID: result.ID, ChatSessionID: source.ChatSessionID, Role: "assistant", Content: humanRequestText(input), MessageKind: pgtype.Text{String: protocol.ChatMessageKindMessage, Valid: true}})
			if err != nil {
				return err
			}
			if err = q.SetChatMessageHumanRequest(ctx, db.SetChatMessageHumanRequestParams{RequestID: result.ID, ChatSessionID: source.ChatSessionID}); err != nil {
				return err
			}
			message.HumanRequestID = result.ID
		}
		details := map[string]string{"human_request_id": util.UUIDToString(result.ID)}
		if source.IssueID.Valid {
			details["comment_id"] = util.UUIDToString(result.ID)
		}
		if source.ChatSessionID.Valid {
			details["chat_session_id"] = util.UUIDToString(source.ChatSessionID)
		}
		if projectID.Valid {
			details["project_id"] = util.UUIDToString(projectID)
		}
		detailJSON, err := json.Marshal(details)
		if err != nil {
			return err
		}
		inbox, err = q.CreateInboxItem(ctx, db.CreateInboxItemParams{ID: result.ID, WorkspaceID: workspaceID, RecipientType: "member", RecipientID: recipient, Type: "human_action_requested", Severity: "action_required", IssueID: source.IssueID, Title: input.Title, Body: pgtype.Text{String: strings.TrimPrefix(humanRequestText(input), input.Title+"\n"), Valid: true}, ActorType: pgtype.Text{String: "agent", Valid: true}, ActorID: source.AgentID, Details: detailJSON})
		changed = err == nil
		return err
	})
	if err != nil || !changed {
		return result, err
	}
	s.publishHumanRequest(result)
	if inbox.ID.Valid && s.Bus != nil {
		s.publishQuickCreateInbox(inbox, util.UUIDToString(workspaceID), util.UUIDToString(source.AgentID), "")
	}
	if comment.ID.Valid && s.Bus != nil {
		s.Bus.Publish(events.Event{Type: commentEvent, WorkspaceID: util.UUIDToString(workspaceID), ActorType: "agent", ActorID: util.UUIDToString(source.AgentID), Payload: map[string]any{"comment": comment}})
	}
	if message.ID.Valid {
		s.publishHumanChatMessage(result, message)
	}
	return result, nil
}

func humanPayloadEqual(raw []byte, input HumanRequestInput) bool {
	var stored HumanRequestInput
	if json.Unmarshal(raw, &stored) != nil {
		return false
	}
	a, err := json.Marshal(stored)
	if err != nil {
		return false
	}
	b, err := json.Marshal(input)
	return err == nil && bytes.Equal(a, b)
}

func lockHumanRequestScope(ctx context.Context, q *db.Queries, source db.AgentTaskQueue, workspaceID pgtype.UUID) (db.ChatSession, error) {
	var chat db.ChatSession
	switch {
	case source.IssueID.Valid:
		_, err := q.LockIssueForDelete(ctx, db.LockIssueForDeleteParams{ID: source.IssueID, WorkspaceID: workspaceID})
		if err != nil {
			return chat, err
		}
		issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: source.IssueID, WorkspaceID: workspaceID})
		if err != nil {
			return chat, err
		}
		if issue.Status == "done" || issue.Status == "cancelled" {
			return chat, ErrHumanRequestConflict
		}
	case source.ChatSessionID.Valid:
		var err error
		chat, err = q.LockChatSessionForEnqueue(ctx, source.ChatSessionID)
		if err != nil {
			return chat, err
		}
		if chat.WorkspaceID != workspaceID || chat.AgentID != source.AgentID || chat.Status != "active" {
			return chat, ErrHumanRequestConflict
		}
	case source.AutopilotRunID.Valid:
		run, err := q.GetAutopilotRun(ctx, source.AutopilotRunID)
		if err != nil {
			return chat, err
		}
		ap, err := q.GetAutopilot(ctx, run.AutopilotID)
		if err != nil {
			return chat, err
		}
		if ap.WorkspaceID != workspaceID {
			return chat, ErrHumanRequestForbidden
		}
	case len(source.Context) > 0:
		if coordination, ok := ProjectCoordination(source); ok {
			projectID, parseErr := util.ParseUUID(coordination.ProjectID)
			if parseErr != nil {
				return chat, ErrHumanRequestInput
			}
			if err := supervisionLock(ctx, q, projectID, workspaceID); err != nil {
				return chat, err
			}
			if _, err := q.LockProjectForDelete(ctx, db.LockProjectForDeleteParams{ID: projectID, WorkspaceID: workspaceID}); err != nil {
				return chat, err
			}
			row, err := q.LockProjectSupervision(ctx, db.LockProjectSupervisionParams{ProjectID: projectID, WorkspaceID: workspaceID})
			if err != nil {
				return chat, err
			}
			if !row.Enabled || row.Revision != coordination.PolicyRevision {
				return chat, ErrHumanRequestConflict
			}
			if coordination.WorkspaceID != util.UUIDToString(workspaceID) {
				return chat, ErrHumanRequestForbidden
			}
		} else if source.AutopilotRunID.Valid {
			// Autopilot ownership is revalidated at enqueue/claim, not inferred
			// from an arbitrary project ID in agent-authored input.
		} else {
			var followup HumanFollowupContext
			if json.Unmarshal(source.Context, &followup) == nil && followup.Type == HumanFollowupContextType && followup.WorkspaceID == util.UUIDToString(workspaceID) {
				return chat, nil
			}
			var quick QuickCreateContext
			if json.Unmarshal(source.Context, &quick) != nil || quick.Type != QuickCreateContextType || quick.WorkspaceID != util.UUIDToString(workspaceID) {
				return chat, ErrHumanRequestForbidden
			}
		}
	default:
		return chat, ErrHumanRequestForbidden
	}
	return chat, nil
}

func (s *TaskService) publishHumanRequest(request db.HumanRequest) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Type: protocol.EventHumanRequestChanged, WorkspaceID: util.UUIDToString(request.WorkspaceID), ActorType: "system", Payload: map[string]any{"request_id": util.UUIDToString(request.ID), "issue_id": util.UUIDToString(request.IssueID), "chat_session_id": util.UUIDToString(request.ChatSessionID), "project_id": util.UUIDToString(request.ProjectID)}})
	}
}

func (s *TaskService) publishHumanChatMessage(request db.HumanRequest, message db.ChatMessage) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Type: protocol.EventChatMessage, WorkspaceID: util.UUIDToString(request.WorkspaceID), ActorType: "agent", ActorID: util.UUIDToString(request.AgentID), ChatSessionID: util.UUIDToString(request.ChatSessionID), Payload: map[string]any{"chat_session_id": util.UUIDToString(request.ChatSessionID), "message": message, "creator_id": util.UUIDToString(request.RecipientID)}})
	}
}

func (s *TaskService) publishHumanResponseComment(request db.HumanRequest, comment db.Comment) {
	s.Bus.Publish(events.Event{Type: protocol.EventCommentCreated, WorkspaceID: util.UUIDToString(request.WorkspaceID), ActorType: "member", ActorID: util.UUIDToString(request.RecipientID), Payload: map[string]any{"comment": comment}})
}

// ListHumanRequests returns recipient/author-owned requests and retires stale waits.
func (s *TaskService) ListHumanRequests(ctx context.Context, params db.ListHumanRequestsParams) ([]db.HumanRequest, error) {
	if err := s.Queries.ExpireHumanRequests(ctx, params.WorkspaceID); err != nil {
		return nil, fmt.Errorf("expire human requests: %w", err)
	}
	rows, err := s.Queries.ListHumanRequests(ctx, params)
	if err != nil {
		return nil, err
	}
	for index, row := range rows {
		rows[index], err = s.RefreshHumanRequestScope(ctx, row)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
