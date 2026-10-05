package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func humanScopeFingerprint(ctx context.Context, q *db.Queries, source db.AgentTaskQueue, workspaceID pgtype.UUID) (string, error) {
	// Comments, running status, and last-activity timestamps are deliberately
	// excluded: delivering the question itself must not invalidate consent.
	var state struct {
		AgentID         pgtype.UUID
		RuntimeID       pgtype.UUID
		Title           string
		Description     string
		AssigneeType    string
		AssigneeID      pgtype.UUID
		ProjectID       pgtype.UUID
		ProjectRevision int64
		ReviewSHA       string
		ChannelRevision pgtype.Int8
		AutopilotID     pgtype.UUID
		AutopilotStatus string
		ExecutionMode   string
	}
	state.AgentID, state.RuntimeID, state.ChannelRevision = source.AgentID, source.RuntimeID, source.ChannelContextRevision
	autopilotID, err := humanRequestAutopilotID(ctx, q, source)
	if err != nil {
		return "", err
	}
	if autopilotID.Valid {
		autopilot, err := q.GetAutopilot(ctx, autopilotID)
		if err != nil {
			return "", err
		}
		if autopilot.WorkspaceID != workspaceID {
			return "", ErrHumanRequestForbidden
		}
		state.AutopilotID, state.AutopilotStatus, state.ExecutionMode = autopilot.ID, autopilot.Status, autopilot.ExecutionMode
		state.Title, state.Description, state.AssigneeType, state.AssigneeID, state.ProjectID = autopilot.Title, autopilot.Description.String, autopilot.AssigneeType, autopilot.AssigneeID, autopilot.ProjectID
	}
	if source.IssueID.Valid {
		issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: source.IssueID, WorkspaceID: workspaceID})
		if err != nil {
			return "", err
		}
		state.Title, state.Description, state.AssigneeType, state.AssigneeID, state.ProjectID = issue.Title, issue.Description.String, issue.AssigneeType.String, issue.AssigneeID, issue.ProjectID
		sha, err := q.GetIssueReviewHeadSha(ctx, issue.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		state.ReviewSHA = sha
	}
	if source.ChatSessionID.Valid {
		chat, err := q.GetChatSession(ctx, source.ChatSessionID)
		if err != nil {
			return "", err
		}
		state.ProjectID = chat.ProjectID
	}
	if coordination, ok := ProjectCoordination(source); ok {
		projectID, err := util.ParseUUID(coordination.ProjectID)
		if err != nil {
			return "", err
		}
		row, err := q.GetProjectSupervision(ctx, db.GetProjectSupervisionParams{ProjectID: projectID, WorkspaceID: workspaceID})
		if err != nil {
			return "", err
		}
		state.ProjectRevision = row.Revision
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func humanRequestAutopilotID(ctx context.Context, q *db.Queries, source db.AgentTaskQueue) (pgtype.UUID, error) {
	if source.AutopilotRunID.Valid {
		run, err := q.GetAutopilotRun(ctx, source.AutopilotRunID)
		return run.AutopilotID, err
	}
	var followup HumanFollowupContext
	if len(source.Context) > 0 && json.Unmarshal(source.Context, &followup) == nil && followup.Type == HumanFollowupContextType && followup.AutopilotID != "" {
		return util.ParseUUID(followup.AutopilotID)
	}
	return pgtype.UUID{}, nil
}

// Autopilot writes lock their assignee first. Keep that order when binding
// consent so a concurrent retarget cannot race the fingerprint or deadlock.
func lockHumanRequestAutopilot(ctx context.Context, q *db.Queries, source db.AgentTaskQueue, workspaceID pgtype.UUID) error {
	id, err := humanRequestAutopilotID(ctx, q, source)
	if err != nil || !id.Valid {
		return err
	}
	_, err = q.LockAutopilotForUpdate(ctx, db.LockAutopilotForUpdateParams{ID: id, WorkspaceID: workspaceID})
	return err
}

// ValidateHumanResponseScope prevents a queued response from authorizing work
// whose material objective or execution host changed after the member decided.
func (s *TaskService) ValidateHumanResponseScope(ctx context.Context, request db.HumanRequest, source db.AgentTaskQueue) error {
	fingerprint, err := humanScopeFingerprint(ctx, s.Queries, source, request.WorkspaceID)
	if err != nil {
		return err
	}
	if fingerprint != request.ScopeFingerprint {
		return ErrHumanRequestConflict
	}
	agent, err := s.Queries.GetAgent(ctx, source.AgentID)
	if err != nil {
		return err
	}
	if agent.RuntimeID != source.RuntimeID {
		return ErrHumanRequestConflict
	}
	allowed, err := memberMayInvokeAgent(ctx, s.Queries, agent, request.RecipientID, request.WorkspaceID)
	if err != nil {
		return err
	}
	if !allowed || agent.ArchivedAt.Valid {
		return ErrHumanRequestConflict
	}
	return nil
}

// RefreshHumanRequestScope gives stale pending cards a terminal state rather
// than asking the member to repeat a decision that can no longer be consumed.
func (s *TaskService) RefreshHumanRequestScope(ctx context.Context, request db.HumanRequest) (db.HumanRequest, error) {
	if request.Status != "pending" {
		return request, nil
	}
	source, err := s.Queries.GetAgentTask(ctx, request.SourceTaskID)
	if err == nil {
		err = s.ValidateHumanResponseScope(ctx, request, source)
	}
	if err == nil {
		return request, nil
	}
	if !errors.Is(err, ErrHumanRequestConflict) && !errors.Is(err, pgx.ErrNoRows) {
		return request, err
	}
	updated, err := s.Queries.CancelStaleHumanRequest(ctx, db.CancelStaleHumanRequestParams{ID: request.ID, WorkspaceID: request.WorkspaceID, Revision: request.Revision, ScopeFingerprint: request.ScopeFingerprint})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.Queries.GetHumanRequest(ctx, db.GetHumanRequestParams{ID: request.ID, WorkspaceID: request.WorkspaceID})
	}
	if err == nil {
		s.publishHumanRequest(updated)
	}
	return updated, err
}
