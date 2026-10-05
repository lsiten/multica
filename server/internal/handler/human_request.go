package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type humanRequestResponse struct {
	db.HumanRequest
	Payload    json.RawMessage `json:"payload"`
	Response   json.RawMessage `json:"response"`
	CanRespond bool            `json:"can_respond"`
}

func humanRequestToResponse(row db.HumanRequest, userID string) humanRequestResponse {
	response := json.RawMessage(row.Response)
	if len(response) == 0 {
		response = json.RawMessage("null")
	}
	return humanRequestResponse{HumanRequest: row, Payload: json.RawMessage(row.Payload), Response: response, CanRespond: uuidToString(row.RecipientID) == userID && row.Status == "pending"}
}

// CreateHumanRequest accepts only the authenticated run's own request.
func (h *Handler) CreateHumanRequest(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, 403, "an active agent task is required")
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "task id")
	if !ok {
		return
	}
	source, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil {
		humanRequestError(w, err)
		return
	}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: source.AgentID, WorkspaceID: parseUUID(workspaceID)})
	if err != nil || uuidToString(agent.ID) != r.Header.Get("X-Agent-ID") {
		writeError(w, 403, "request source does not match the authenticated task")
		return
	}
	var input service.HumanRequestInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, 400, "invalid human request body")
		return
	}
	row, err := h.TaskService.CreateHumanRequest(r.Context(), source, agent.WorkspaceID, input)
	if err != nil {
		humanRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, humanRequestToResponse(row, ""))
}

func (h *Handler) GetHumanRequest(w http.ResponseWriter, r *http.Request) {
	row, actor, ok := h.loadHumanRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, humanRequestToResponse(row, actor))
}

func (h *Handler) loadHumanRequest(w http.ResponseWriter, r *http.Request) (db.HumanRequest, string, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return db.HumanRequest{}, "", false
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "requestId"), "request id")
	if !ok {
		return db.HumanRequest{}, "", false
	}
	if err := h.Queries.ExpireHumanRequests(r.Context(), parseUUID(workspaceID)); err != nil {
		humanRequestError(w, err)
		return db.HumanRequest{}, "", false
	}
	row, err := h.Queries.GetHumanRequest(r.Context(), db.GetHumanRequestParams{ID: id, WorkspaceID: parseUUID(workspaceID)})
	if err != nil {
		humanRequestError(w, err)
		return row, "", false
	}
	row, err = h.TaskService.RefreshHumanRequestScope(r.Context(), row)
	if err != nil {
		humanRequestError(w, err)
		return row, "", false
	}
	kind, actor := h.resolveActor(r, requestUserID(r), workspaceID)
	if kind == "agent" && row.AgentID == parseUUID(actor) {
		return row, "", true
	}
	if kind == "member" && uuidToString(row.RecipientID) == actor {
		return row, actor, true
	}
	// An issue is shared workspace work; other readers see the request, but
	// cannot submit a decision or read the designated member's answer.
	if kind == "member" && row.IssueID.Valid {
		if _, ok := h.loadIssueForUser(w, r, uuidToString(row.IssueID)); !ok {
			return row, "", false
		}
		row.Response = nil
		return row, "", true
	}
	writeError(w, http.StatusForbidden, "only the requesting agent and designated member can access this request")
	return row, "", false
}

func (h *Handler) ListHumanRequests(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	kind, actor := h.resolveActor(r, requestUserID(r), workspaceID)
	params := db.ListHumanRequestsParams{WorkspaceID: parseUUID(workspaceID)}
	if kind == "agent" {
		params.AgentID = parseUUID(actor)
	} else {
		params.MemberID = parseUUID(actor)
	}
	for key, dst := range map[string]*pgtype.UUID{"issue_id": &params.IssueID, "chat_session_id": &params.ChatSessionID, "project_id": &params.ProjectID} {
		if value := r.URL.Query().Get(key); value != "" {
			id, ok := parseUUIDOrBadRequest(w, value, key)
			if !ok {
				return
			}
			*dst = id
		}
	}
	rows, err := h.TaskService.ListHumanRequests(r.Context(), params)
	if err != nil {
		humanRequestError(w, err)
		return
	}
	result := make([]humanRequestResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, humanRequestToResponse(row, uuidToString(params.MemberID)))
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) RespondHumanRequest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Actor-Source") == "task_token" {
		writeError(w, 403, "a member must answer this request")
		return
	}
	row, actor, ok := h.loadHumanRequest(w, r)
	if !ok {
		return
	}
	if actor == "" || uuidToString(row.RecipientID) != actor {
		writeError(w, 403, "only the designated member may answer")
		return
	}
	var answer service.HumanRequestAnswer
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		writeError(w, 400, "invalid human response body")
		return
	}
	result, err := h.TaskService.RespondHumanRequest(r.Context(), row, parseUUID(actor), answer)
	if err != nil {
		humanRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, humanRequestToResponse(result, actor))
}

// ReplyHumanRequest accepts ordinary text only through an explicit versioned request binding.
func (h *Handler) ReplyHumanRequest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Actor-Source") == "task_token" {
		writeError(w, 403, "a member must answer this request")
		return
	}
	row, actor, ok := h.loadHumanRequest(w, r)
	if !ok {
		return
	}
	if actor == "" || uuidToString(row.RecipientID) != actor {
		writeError(w, 403, "only the designated member may answer")
		return
	}
	var input service.HumanTextReply
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, 400, "invalid text reply")
		return
	}
	if _, ok := parseUUIDOrBadRequest(w, input.ScopeID, "scope_id"); !ok {
		return
	}
	result, err := h.TaskService.RespondHumanRequestText(r.Context(), row, parseUUID(actor), input)
	if err != nil {
		humanRequestError(w, err)
		return
	}
	var response struct {
		Origin *service.HumanReplyOrigin `json:"origin"`
	}
	if err := json.Unmarshal(result.Response, &response); err != nil {
		writeError(w, 500, "failed to read reply receipt")
		return
	}
	writeJSON(w, 200, map[string]any{"request": humanRequestToResponse(result, actor), "reply": response.Origin, "task_id": uuidToPtr(result.ResponseTaskID)})
}

func humanRequestError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrHumanRequestInput):
		writeError(w, 400, err.Error())
	case errors.Is(err, service.ErrHumanRequestForbidden):
		writeError(w, 403, "human request access denied")
	case errors.Is(err, service.ErrHumanRequestConflict), errors.Is(err, service.ErrDuplicatePendingTask):
		writeError(w, 409, "request changed or cannot continue; refresh the request before trying again")
	case errors.Is(err, service.ErrChatTaskAgentArchived), errors.Is(err, service.ErrChatTaskAgentNoRuntime), errors.Is(err, service.ErrChatSessionArchived):
		writeError(w, 409, "the requesting agent or conversation is unavailable; ask for an updated request")
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, 404, "request or its source is no longer available")
	default:
		writeError(w, 500, "could not process the request; retry")
	}
}
