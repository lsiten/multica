package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

var phoneInboundEventIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// PhoneInboundRequest is the normalized webhook contract used by a phone
// gateway. The gateway authenticates itself with MULTICA_PHONE_INBOUND_TOKEN;
// provider-specific signatures are verified at the gateway adapter boundary.
type PhoneInboundRequest struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	From        string `json:"from"`
	CallSID     string `json:"call_sid"`
	EventID     string `json:"event_id"`
	Transcript  string `json:"transcript"`
}

// ReceivePhoneInbound acknowledges a gateway callback and turns a transcript
// into an Agent chat turn. The response is TwiML-compatible XML so a Twilio
// adapter can answer immediately; the Agent task runs asynchronously.
func (h *Handler) ReceivePhoneInbound(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(h.cfg.PhoneInboundToken)
	presented := strings.TrimSpace(r.Header.Get("X-Phone-Inbound-Token"))
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(presented)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req PhoneInboundRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || strings.TrimSpace(req.WorkspaceID) == "" || strings.TrimSpace(req.AgentID) == "" || strings.TrimSpace(req.From) == "" || strings.TrimSpace(req.CallSID) == "" || !phoneInboundEventIDPattern.MatchString(strings.TrimSpace(req.EventID)) || strings.TrimSpace(req.Transcript) == "" || len(req.Transcript) > 64<<10 {
		http.Error(w, "invalid inbound phone event", http.StatusBadRequest)
		return
	}
	ws, ok := parseUUIDOrBadRequest(w, req.WorkspaceID, "workspace_id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: ws})
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to load agent", 500)
		return
	}
	if agent.ArchivedAt.Valid || !agent.OwnerID.Valid {
		http.Error(w, "agent cannot receive phone events", http.StatusConflict)
		return
	}
	identity, err := h.Queries.GetAgentIdentity(r.Context(), db.GetAgentIdentityParams{AgentID: agent.ID, WorkspaceID: ws})
	if err != nil || !identity.Phone.Valid || identity.Phone.String != strings.TrimSpace(req.From) {
		http.Error(w, "caller identity does not match agent", http.StatusForbidden)
		return
	}
	var duplicate bool
	if h.DB != nil {
		if err := h.DB.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM chat_message m JOIN chat_session s ON s.id=m.chat_session_id WHERE s.workspace_id=$1 AND m.content LIKE $2)`, ws, "[phone_event:"+req.EventID+"]%").Scan(&duplicate); err != nil {
			http.Error(w, "failed to inspect inbound event", 500)
			return
		}
	}
	if !duplicate {
		session, err := h.Queries.CreateChatSession(r.Context(), db.CreateChatSessionParams{ID: dbid.NewV7(), WorkspaceID: ws, AgentID: agent.ID, CreatorID: agent.OwnerID, Title: fmt.Sprintf("Inbound call %s", req.CallSID), IsAgentIntro: false, ProjectID: pgtype.UUID{}})
		if err != nil {
			http.Error(w, "failed to create phone chat", 500)
			return
		}
		content := "[phone_event:" + req.EventID + "]\nInbound call " + req.CallSID + " from " + req.From + ". Summarize the call and identify follow-up actions. Transcript:\n" + req.Transcript
		if _, err = h.Queries.CreateChatMessage(r.Context(), db.CreateChatMessageParams{ID: dbid.NewV7(), ChatSessionID: session.ID, Role: "user", Content: content, TaskID: pgtype.UUID{}, MessageKind: pgtype.Text{String: "phone_inbound", Valid: true}}); err != nil {
			http.Error(w, "failed to record inbound call", 500)
			return
		}
		if _, err = h.TaskService.EnqueueChatTask(r.Context(), session, agent.OwnerID, false); err != nil {
			http.Error(w, "failed to enqueue inbound call", 500)
			return
		}
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say>Thank you. Your call has been received.</Say><Hangup/></Response>`))
}
