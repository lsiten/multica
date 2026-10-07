package application

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func applicationCommandAuthorized(ctx context.Context, q *db.Queries, command protocol.ApplicationControlCommand) (bool, error) {
	if command.AccessMemberID == "" || command.AccessUserID == "" {
		return false, nil
	}
	workspaceID, err := util.ParseUUID(command.WorkspaceID)
	if err != nil {
		return false, err
	}
	userID, err := util.ParseUUID(command.AccessUserID)
	if err != nil {
		return false, err
	}
	member, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{WorkspaceID: workspaceID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil || util.UUIDToString(member.ID) != command.AccessMemberID {
		return false, err
	}
	runtimeID, err := util.ParseUUID(command.RuntimeID)
	if err != nil {
		return false, err
	}
	runtime, err := q.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{WorkspaceID: workspaceID, ID: runtimeID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil || !runtime.OwnerID.Valid || runtime.OwnerID != member.UserID && runtime.Visibility != "public" {
		return false, err
	}
	if command.AccessAgentID == "" {
		return true, nil
	}
	agentID, err := util.ParseUUID(command.AccessAgentID)
	if err != nil {
		return false, err
	}
	agent, err := q.GetAgent(ctx, agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil || agent.WorkspaceID != workspaceID || agent.ArchivedAt.Valid {
		return false, err
	}
	applicationID, err := util.ParseUUID(command.ApplicationID)
	if err != nil {
		return false, err
	}
	app, err := q.GetApplication(ctx, db.GetApplicationParams{ID: applicationID, WorkspaceID: workspaceID})
	if err != nil {
		return false, err
	}
	allowed, err := q.AgentMayUseProject(ctx, db.AgentMayUseProjectParams{LeadID: agentID, ID: app.ProjectID})
	return allowed.Valid && allowed.Bool, err
}
