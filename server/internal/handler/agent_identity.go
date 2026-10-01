package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

var agentIdentityPhonePattern = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)
var agentIdentityMainlandPhonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

// AgentIdentityResponse is the non-secret identity manifest an agent may use
// when acting on the user's behalf. Payment credentials and authorization
// limits are intentionally not part of the identity contract.
type AgentIdentityResponse struct {
	AgentID   string `json:"agent_id"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type updateAgentIdentityRequest struct {
	Email string `json:"email"`
	Phone string `json:"phone"`
}

const agentIdentityActivityUpdated = "agent_identity_updated"

func agentIdentityResponse(identity db.AgentIdentity, agentID pgtype.UUID) AgentIdentityResponse {
	response := AgentIdentityResponse{
		AgentID: uuidToString(agentID),
		Email:   identity.Email.String,
		Phone:   identity.Phone.String,
	}
	if identity.UpdatedAt.Valid {
		response.UpdatedAt = identity.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	return response
}

func normalizeAgentIdentity(req updateAgentIdentityRequest) (db.UpsertAgentIdentityParams, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email != "" {
		if len(email) > 320 {
			return db.UpsertAgentIdentityParams{}, errors.New("identity email is too long")
		}
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email || strings.ContainsAny(email, "\r\n") {
			return db.UpsertAgentIdentityParams{}, errors.New("identity email is invalid")
		}
	}
	phone := strings.TrimSpace(req.Phone)
	if agentIdentityMainlandPhonePattern.MatchString(phone) {
		phone = "+86" + phone
	}
	if phone != "" && !agentIdentityPhonePattern.MatchString(phone) {
		return db.UpsertAgentIdentityParams{}, errors.New("identity phone must use E.164 format")
	}
	return db.UpsertAgentIdentityParams{
		Email: pgtype.Text{String: email, Valid: email != ""},
		Phone: pgtype.Text{String: phone, Valid: phone != ""},
		// Legacy payment columns remain in storage for migration compatibility,
		// but are always cleared when an identity is written.
		WalletAddress:  pgtype.Text{},
		BudgetUsdTicks: 0,
	}, nil
}

func (h *Handler) loadManagedAgentForIdentity(w http.ResponseWriter, r *http.Request) (db.Agent, bool) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "agent identity requires a human actor")
		return db.Agent{}, false
	}
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Agent{}, false
	}
	if !h.canManageAgent(w, r, agent) {
		return db.Agent{}, false
	}
	return agent, true
}

func (h *Handler) GetAgentIdentity(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadManagedAgentForIdentity(w, r)
	if !ok {
		return
	}
	identity, err := h.Queries.GetAgentIdentity(r.Context(), db.GetAgentIdentityParams{
		AgentID: agent.ID, WorkspaceID: agent.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, AgentIdentityResponse{AgentID: uuidToString(agent.ID)})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent identity")
		return
	}
	writeJSON(w, http.StatusOK, agentIdentityResponse(identity, agent.ID))
}

func (h *Handler) UpdateAgentIdentity(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadManagedAgentForIdentity(w, r)
	if !ok {
		return
	}
	var req *updateAgentIdentityRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent identity")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid agent identity")
		return
	}
	if req == nil {
		writeError(w, http.StatusBadRequest, "invalid agent identity")
		return
	}
	params, err := normalizeAgentIdentity(*req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params.AgentID = agent.ID
	params.WorkspaceID = agent.WorkspaceID
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent identity")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	// Match workspace teardown's parent lock before writing this FK-free row.
	if _, err := qtx.LockWorkspaceForChatSessionCreate(r.Context(), agent.WorkspaceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "workspace not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to lock identity workspace")
		}
		return
	}
	lockedAgent, err := qtx.GetAgentForUpdate(r.Context(), agent.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to lock agent identity")
		}
		return
	}
	if lockedAgent.WorkspaceID != agent.WorkspaceID || lockedAgent.Kind != "user" {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if !h.canManageAgent(w, r, lockedAgent) {
		return
	}
	identity, err := qtx.UpsertAgentIdentity(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent identity")
		return
	}
	details, _ := json.Marshal(map[string]any{
		"agent_id": uuidToString(agent.ID),
		"fields":   []string{"email", "phone"},
	})
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		ID: dbid.NewV7(), WorkspaceID: agent.WorkspaceID,
		IssueID: pgtype.UUID{}, ActorType: pgtype.Text{String: "member", Valid: true},
		ActorID: parseUUID(requestUserID(r)), Action: agentIdentityActivityUpdated, Details: details,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to audit agent identity update")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent identity")
		return
	}
	writeJSON(w, http.StatusOK, agentIdentityResponse(identity, agent.ID))
}
