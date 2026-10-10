package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type jevDecisionSummary struct {
	ID              string `json:"id"`
	WorkspaceID     string `json:"workspace_id"`
	TaskID          string `json:"task_id"`
	AgentID         string `json:"agent_id"`
	AgentName       string `json:"agent_name"`
	IssueIdentifier string `json:"issue_identifier"`
	Tool            string `json:"tool"`
	Source          string `json:"source"`
	Model           string `json:"model"`
	ResultClass     string `json:"result_class"`
	ErrorCode       string `json:"error_code"`
	StartedAt       string `json:"started_at"`
	DurationMS      int64  `json:"duration_ms"`
}

// ReportJevDecisionLog derives audit ownership from the authenticated task.
func (h *Handler) ReportJevDecisionLog(w http.ResponseWriter, r *http.Request) {
	task, workspaceID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, chi.URLParam(r, "taskId"))
	if !ok {
		return
	}
	if !task.RuntimeID.Valid {
		writeError(w, 403, "task has no assigned runtime")
		return
	}
	if _, ok := h.requireDaemonRuntimeAccess(w, r, uuidToString(task.RuntimeID)); !ok {
		return
	}
	var report protocol.JevDecisionLog
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || decoder.Decode(new(any)) != io.EOF || report.Validate() != nil {
		writeError(w, 400, "invalid Jev decision log")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, report.ID, "decision_id")
	if !ok {
		return
	}
	wsID := parseUUID(workspaceID)
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: task.AgentID, WorkspaceID: wsID})
	if err != nil {
		writeError(w, 404, "agent not found")
		return
	}
	issueIdentifier := ""
	if task.IssueID.Valid {
		issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: task.IssueID, WorkspaceID: wsID})
		if err != nil {
			writeError(w, 404, "issue not found")
			return
		}
		workspace, err := h.Queries.GetWorkspace(r.Context(), wsID)
		if err != nil {
			writeError(w, 500, "failed to load workspace")
			return
		}
		issueIdentifier = workspace.IssuePrefix + "-" + strconv.Itoa(int(issue.Number))
	}
	if report.Requests == nil {
		report.Requests = []protocol.JevDecisionRequestLog{}
	}
	payload, err := json.Marshal(report)
	if err != nil {
		writeError(w, 400, "invalid Jev decision log")
		return
	}
	phase := int32(0)
	if report.CompletedAt != nil {
		phase = 1
	}
	tx, qtx, ok := h.beginExecutionCallback(w, r, task.ID)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	_, err = qtx.UpsertJevDecisionLog(r.Context(), db.UpsertJevDecisionLogParams{ID: id, WorkspaceID: wsID, TaskID: task.ID, AgentID: task.AgentID, AgentName: agent.Name, IssueIdentifier: issueIdentifier, Tool: report.Tool, Source: report.Source, Model: report.Model, ResultClass: report.ResultClass, ErrorCode: report.ErrorCode, StartedAt: pgtype.Timestamptz{Time: report.StartedAt, Valid: true}, DurationMs: report.DurationMS, Phase: phase, Payload: payload})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "Jev decision belongs to another task")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to store Jev decision log")
		return
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	writeJSON(w, 200, map[string]string{"id": report.ID})
}

func (h *Handler) jevLogWorkspace(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Jev decision logs require a human administrator")
		return pgtype.UUID{}, false
	}
	id, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace_id")
	if !ok {
		return id, false
	}
	_, ok = h.requireWorkspaceRole(w, r, uuidToString(id), "workspace not found", "owner", "admin")
	return id, ok
}

// ListJevDecisionLogs returns bounded pages without loading large payloads.
func (h *Handler) ListJevDecisionLogs(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.jevLogWorkspace(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()
	limit, offset := int32(20), int32(0)
	for key, target := range map[string]*int32{"limit": &limit, "offset": &offset} {
		if raw := values.Get(key); raw != "" {
			number, err := strconv.ParseInt(raw, 10, 32)
			if err != nil || number < 0 {
				writeError(w, 400, "invalid pagination")
				return
			}
			*target = int32(number)
		}
	}
	if limit < 1 || limit > 100 {
		writeError(w, 400, "limit must be between 1 and 100")
		return
	}
	query, agentQuery := strings.TrimSpace(values.Get("q")), strings.TrimSpace(values.Get("agent"))
	status, source := values.Get("result_class"), values.Get("source")
	if len(query) > 500 || len(agentQuery) > 256 || !jevLogFilterValue(status, "running", "success", "error", "rejected") || !jevLogFilterValue(source, "agent_context", "local", "remote", "system_one") {
		writeError(w, 400, "invalid Jev log filter")
		return
	}
	times := map[string]pgtype.Timestamptz{}
	for _, key := range []string{"from", "to", "as_of"} {
		if raw := values.Get(key); raw != "" {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				writeError(w, 400, "invalid log time range")
				return
			}
			times[key] = pgtype.Timestamptz{Time: parsed, Valid: true}
		}
	}
	if times["from"].Valid && times["to"].Valid && times["to"].Time.Before(times["from"].Time) {
		writeError(w, 400, "invalid log time range")
		return
	}
	asOf := time.Now().UTC()
	if times["as_of"].Valid && times["as_of"].Time.Before(asOf) {
		asOf = times["as_of"].Time
	}
	snapshot := pgtype.Timestamptz{Time: asOf, Valid: true}
	rows, err := h.Queries.ListJevDecisionLogs(r.Context(), db.ListJevDecisionLogsParams{WorkspaceID: wsID, AsOf: snapshot, Query: query, AgentQuery: agentQuery, ResultClass: status, Source: source, FromTime: times["from"], ToTime: times["to"], PageLimit: limit, PageOffset: offset})
	if err != nil {
		writeError(w, 500, "failed to load Jev decision logs")
		return
	}
	total, err := h.Queries.CountJevDecisionLogs(r.Context(), db.CountJevDecisionLogsParams{WorkspaceID: wsID, AsOf: snapshot, Query: query, AgentQuery: agentQuery, ResultClass: status, Source: source, FromTime: times["from"], ToTime: times["to"]})
	if err != nil {
		writeError(w, 500, "failed to count Jev decision logs")
		return
	}
	items := make([]jevDecisionSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, jevDecisionSummary{ID: uuidToString(row.ID), WorkspaceID: uuidToString(wsID), TaskID: uuidToString(row.TaskID), AgentID: uuidToString(row.AgentID), AgentName: row.AgentName, IssueIdentifier: row.IssueIdentifier, Tool: row.Tool, Source: row.Source, Model: row.Model, ResultClass: row.ResultClass, ErrorCode: row.ErrorCode, StartedAt: timestampToString(row.StartedAt), DurationMS: row.DurationMs})
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset, "as_of": asOf.Format(time.RFC3339Nano)})
}

func jevLogFilterValue(value string, allowed ...string) bool {
	if value == "" {
		return true
	}
	for _, option := range allowed {
		if value == option {
			return true
		}
	}
	return false
}

// GetJevDecisionLog returns the input, output and all attempts for one decision.
func (h *Handler) GetJevDecisionLog(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.jevLogWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "decisionId"), "decision_id")
	if !ok {
		return
	}
	row, err := h.Queries.GetJevDecisionLog(r.Context(), db.GetJevDecisionLogParams{ID: id, WorkspaceID: wsID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "Jev decision log not found")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to load Jev decision log")
		return
	}
	var record protocol.JevDecisionLog
	if json.Unmarshal(row.Payload, &record) != nil {
		writeError(w, 500, "invalid stored Jev decision log")
		return
	}
	writeJSON(w, 200, struct {
		protocol.JevDecisionLog
		WorkspaceID     string `json:"workspace_id"`
		TaskID          string `json:"task_id"`
		AgentID         string `json:"agent_id"`
		AgentName       string `json:"agent_name"`
		IssueIdentifier string `json:"issue_identifier"`
	}{record, uuidToString(row.WorkspaceID), uuidToString(row.TaskID), uuidToString(row.AgentID), row.AgentName, row.IssueIdentifier})
}
