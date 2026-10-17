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
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// GroupChatMemberResponse is one participant in a group chat.
type GroupChatMemberResponse struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
	JoinedAt   string `json:"joined_at"`
}

func groupMemberToResponse(m db.ChatParticipant) GroupChatMemberResponse {
	return GroupChatMemberResponse{
		MemberType: m.MemberType,
		MemberID:   uuidToString(m.MemberID),
		JoinedAt:   m.JoinedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ListGroupParticipants returns the human/agent members of a group chat.
func (h *Handler) ListGroupParticipants(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	sessionID := chi.URLParam(r, "sessionId")
	session, ok := h.loadChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}
	if session.Kind != "group" {
		writeError(w, http.StatusBadRequest, "not a group chat")
		return
	}
	members, err := h.Queries.ListGroupParticipants(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list participants")
		return
	}
	out := make([]GroupChatMemberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, groupMemberToResponse(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"participants": out})
}

type addGroupParticipantRequest struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
}

// AddGroupParticipant adds a member to a group chat.
func (h *Handler) AddGroupParticipant(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	sessionID := chi.URLParam(r, "sessionId")
	session, ok := h.loadChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}
	if session.Kind != "group" {
		writeError(w, http.StatusBadRequest, "not a group chat")
		return
	}
	var req addGroupParticipantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MemberType != "member" && req.MemberType != "agent" {
		writeError(w, http.StatusBadRequest, "member_type must be member or agent")
		return
	}
	memberID, ok := parseUUIDOrBadRequest(w, req.MemberID, "member_id")
	if !ok {
		return
	}
	m, err := h.Queries.AddGroupParticipant(r.Context(), db.AddGroupParticipantParams{
		ChatSessionID: session.ID,
		MemberType:    req.MemberType,
		MemberID:      memberID,
	})
	if err != nil {
		// ON CONFLICT DO NOTHING returns no rows for an existing member.
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "participant already added")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to add participant")
		return
	}
	writeJSON(w, http.StatusCreated, groupMemberToResponse(m))
}

// RemoveGroupParticipant removes a member from a group chat.
func (h *Handler) RemoveGroupParticipant(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	sessionID := chi.URLParam(r, "sessionId")
	session, ok := h.loadChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}
	if session.Kind != "group" {
		writeError(w, http.StatusBadRequest, "not a group chat")
		return
	}
	memberType := chi.URLParam(r, "memberType")
	memberID := chi.URLParam(r, "memberID")
	memberUUID, ok := parseUUIDOrBadRequest(w, memberID, "member_id")
	if !ok {
		return
	}
	if err := h.Queries.RemoveGroupParticipant(r.Context(), db.RemoveGroupParticipantParams{
		ChatSessionID: session.ID,
		MemberType:    memberType,
		MemberID:      memberUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove participant")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

type createGroupChatRequest struct {
	AgentID      string  `json:"agent_id"`
	Title        string  `json:"title"`
	ProjectID    *string `json:"project_id"`
	SquadID      *string `json:"squad_id"`
	Participants []struct {
		MemberType string `json:"member_type"`
		MemberID   string `json:"member_id"`
	} `json:"participants"`
}

// CreateGroupChat creates a manually-created group chat (kind='group'). Unlike a
// project's default group (ensureProjectGroupChat), it is not tied to a project
// (project_id is optional) and is not a project default (is_project_default=false),
// so it can be archived or deleted like an ordinary chat. The caller is always
// added as a member participant; any requested participants are added on top,
// deduplicated by the unique index on (chat_session_id, member_type, member_id).
func (h *Handler) CreateGroupChat(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())

	var req createGroupChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	projectID := pgtype.UUID{Valid: false}
	if req.ProjectID != nil && strings.TrimSpace(*req.ProjectID) != "" {
		projectID, ok = parseUUIDOrBadRequest(w, strings.TrimSpace(*req.ProjectID), "project_id")
		if !ok {
			return
		}
	}
	squadID := pgtype.UUID{Valid: false}
	if req.SquadID != nil && strings.TrimSpace(*req.SquadID) != "" {
		squadID, ok = parseUUIDOrBadRequest(w, strings.TrimSpace(*req.SquadID), "squad_id")
		if !ok {
			return
		}
	}

	// Verify the agent exists in the workspace and is active.
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "agent is archived")
		return
	}
	if err := h.validateChatExecutionScope(r.Context(), workspaceUUID, agent.ID, projectID, squadID); err != nil {
		writeChatScopeError(w, err)
		return
	}
	// Invocation gate: starting a chat produces agent runs, so it uses the invoke
	// permission (MUL-3963), not the softer view gate.
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if !h.canInvokeAgent(r.Context(), agent, actorType, actorID, h.invokeOriginatorFromRequest(r, actorType, actorID), workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}

	// Validate participants up front so a bad member_id fails before any row is
	// written. The creator is always a member; requested participants are added on
	// top and deduped by the unique index at insert time.
	creatorID := parseUUID(userID)
	type resolvedParticipant struct {
		memberType string
		memberID   pgtype.UUID
	}
	resolved := []resolvedParticipant{{memberType: "member", memberID: creatorID}}
	for _, p := range req.Participants {
		if p.MemberType != "member" && p.MemberType != "agent" {
			writeError(w, http.StatusBadRequest, "member_type must be member or agent")
			return
		}
		memberID, ok := parseUUIDOrBadRequest(w, p.MemberID, "member_id")
		if !ok {
			return
		}
		if p.MemberType == "agent" {
			participantAgent, participantErr := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
				ID: memberID, WorkspaceID: workspaceUUID,
			})
			if errors.Is(participantErr, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "participant agent not found")
				return
			}
			if participantErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to load participant agent")
				return
			}
			if participantAgent.ArchivedAt.Valid {
				writeError(w, http.StatusBadRequest, "participant agent is archived")
				return
			}
		}
		resolved = append(resolved, resolvedParticipant{memberType: p.MemberType, memberID: memberID})
	}

	// Create inside a tx that first takes a FOR KEY SHARE lock on the workspace
	// row so a session cannot be created into a workspace whose delete is in
	// progress (mirrors CreateChatSession; see #5219 create/delete protocol).
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	if _, err := qtx.LockWorkspaceForChatSessionCreate(r.Context(), workspaceUUID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to lock workspace")
		return
	}
	if projectID.Valid {
		if _, err := qtx.LockProjectForChatSessionCreate(r.Context(), db.LockProjectForChatSessionCreateParams{
			ID:          projectID,
			WorkspaceID: workspaceUUID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "project not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to lock project")
			return
		}
	}
	if squadID.Valid {
		if _, err := qtx.GetSquadInWorkspaceForChat(r.Context(), db.GetSquadInWorkspaceForChatParams{ID: squadID, WorkspaceID: workspaceUUID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "squad not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load squad")
			return
		}
	}

	session, err := qtx.CreateGroupChatSession(r.Context(), db.CreateGroupChatSessionParams{
		WorkspaceID:      workspaceUUID,
		AgentID:          agentID,
		CreatorID:        creatorID,
		Title:            req.Title,
		IsAgentIntro:     false,
		IsProjectDefault: false,
		ProjectID:        projectID,
		SquadID:          squadID,
		ID:               dbid.NewV7(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create group chat")
		return
	}
	session, err = qtx.MarkChatSessionExplicitlyCreated(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark group chat explicit")
		return
	}

	// Add the creator and requested participants in the same transaction.
	// ON CONFLICT DO NOTHING dedups, so an already-present member is a no-op
	// (ErrNoRows) rather than an error.
	for _, p := range resolved {
		if _, err := qtx.AddGroupParticipant(r.Context(), db.AddGroupParticipantParams{
			ChatSessionID: session.ID,
			MemberType:    p.memberType,
			MemberID:      p.memberID,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			// Non-duplicate participant errors are non-fatal: the session is
			// already created and a missing participant is a soft failure.
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit group chat create")
		return
	}

	writeJSON(w, http.StatusCreated, chatSessionToResponse(session))
}

// archiveProjectGroupOnTerminalStatus archives a project's default group when the
// project reaches a terminal status (completed/cancelled). The group is never
// archived manually; this is the only path to archive it. Best-effort: the
// project status update already committed.
func (h *Handler) archiveProjectGroupOnTerminalStatus(ctx context.Context, project db.Project) {
	if project.Status != "completed" && project.Status != "cancelled" {
		return
	}
	_ = h.Queries.ArchiveProjectDefaultGroup(ctx, project.ID)
}

// isGroupChatManualArchive reports whether archiving this session is a manual
// (not project-driven) archive of a group chat, which is disallowed.
func isGroupChatManualArchive(session db.ChatSession) bool {
	return session.Kind == "group" && session.IsProjectDefault
}

var _ pgtype.UUID

// ensureProjectGroupChat creates a project's default group chat when it does not
// exist yet. Default members: the project lead (when it is an agent) and the
// caller (the project creator). Idempotent: a re-call returns the existing
// group. Returns the session, or an error when the project has no agent.
func (h *Handler) ensureProjectGroupChat(ctx context.Context, project db.Project, creator pgtype.UUID) (db.ChatSession, error) {
	if existing, err := h.Queries.GetProjectDefaultGroup(ctx, project.ID); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.ChatSession{}, err
	}
	if !project.LeadType.Valid || project.LeadType.String != "agent" || !project.LeadID.Valid {
		return db.ChatSession{}, errors.New("project lead is not an agent; cannot create a group chat yet")
	}
	session, err := h.Queries.CreateGroupChatSession(ctx, db.CreateGroupChatSessionParams{
		WorkspaceID:      project.WorkspaceID,
		AgentID:          project.LeadID,
		CreatorID:        creator,
		Title:            "Group",
		IsAgentIntro:     false,
		IsProjectDefault: true,
		ProjectID:        project.ID,
	})
	if err != nil {
		return db.ChatSession{}, err
	}
	// Add the creator and the lead as participants (best-effort; dedup is
	// enforced by the unique index on (session, member_type, member_id)).
	_, _ = h.Queries.AddGroupParticipant(ctx, db.AddGroupParticipantParams{
		ChatSessionID: session.ID,
		MemberType:    "member",
		MemberID:      creator,
	})
	_, _ = h.Queries.AddGroupParticipant(ctx, db.AddGroupParticipantParams{
		ChatSessionID: session.ID,
		MemberType:    "agent",
		MemberID:      project.LeadID,
	})
	return session, nil
}
