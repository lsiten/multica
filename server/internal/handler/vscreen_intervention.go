package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ReportVscreenIntervention accepts only the owning daemon and verifies its report through the current socket.
func (h *Handler) ReportVscreenIntervention(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntimeAccess(w, r, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	daemonID := middleware.DaemonIDFromContext(r.Context())
	if daemonID == "" || !rt.DaemonID.Valid || rt.DaemonID.String != daemonID || h.DaemonHub == nil {
		writeVscreenReason(w, http.StatusForbidden, "permission_denied")
		return
	}
	var report protocol.VscreenIntervention
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || report.Validate() != nil || report.RuntimeID != uuidToString(rt.ID) || report.WorkspaceID != uuidToString(rt.WorkspaceID) {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_report")
		return
	}
	scope, err := h.DaemonHub.VscreenConnection(report.VscreenEnvelope, daemonID)
	if err != nil {
		writeVscreenError(w, err)
		return
	}
	row, err := h.ingestVscreenIntervention(r.Context(), scope, report)
	if err != nil {
		status, reason := interventionReportError(err)
		writeVscreenReason(w, status, reason)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// ListVscreenInterventions returns persisted history even while the host is offline.
func (h *Handler) ListVscreenInterventions(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "vscreen", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	rows, err := h.Queries.ListVscreenInterventions(r.Context(), db.ListVscreenInterventionsParams{WorkspaceID: rt.WorkspaceID, RuntimeID: rt.ID})
	if err != nil {
		writeInterventionError(w, err)
		return
	}
	visible := make([]db.RuntimeVscreenIntervention, 0, len(rows))
	for _, row := range rows {
		allowed, err := h.canReadVscreenIntervention(r.Context(), requestUserID(r), row)
		if err != nil {
			writeInterventionError(w, err)
			return
		}
		if allowed {
			visible = append(visible, row)
		}
	}
	writeJSON(w, http.StatusOK, visible)
}

func (h *Handler) loadVscreenIntervention(w http.ResponseWriter, r *http.Request) (db.RuntimeVscreenIntervention, db.AgentRuntime, bool) {
	var empty db.RuntimeVscreenIntervention
	var rt db.AgentRuntime
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task_id")
	if !ok {
		return empty, rt, false
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "intervention_id")
	if !ok {
		return empty, rt, false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil {
		writeInterventionError(w, err)
		return empty, rt, false
	}
	if !task.RuntimeID.Valid {
		writeVscreenReason(w, http.StatusNotFound, "not_found")
		return empty, rt, false
	}
	rt, _, ok = h.requireRuntimeReadAccess(w, r, "vscreen", uuidToString(task.RuntimeID))
	if !ok {
		return empty, rt, false
	}
	row, err := h.Queries.GetVscreenIntervention(r.Context(), db.GetVscreenInterventionParams{ID: id, WorkspaceID: rt.WorkspaceID})
	if err != nil {
		writeInterventionError(w, err)
		return empty, rt, false
	}
	if row.SourceTaskID != task.ID || row.RuntimeID != rt.ID {
		writeVscreenReason(w, http.StatusNotFound, "not_found")
		return empty, rt, false
	}
	allowed, err := h.canReadVscreenIntervention(r.Context(), requestUserID(r), row)
	if err != nil {
		writeInterventionError(w, err)
		return empty, rt, false
	}
	if !allowed {
		writeVscreenReason(w, http.StatusForbidden, "permission_denied")
		return empty, rt, false
	}
	return row, rt, true
}

// GetVscreenIntervention reads one stopped run's durable handoff.
func (h *Handler) GetVscreenIntervention(w http.ResponseWriter, r *http.Request) {
	row, _, ok := h.loadVscreenIntervention(w, r)
	if ok {
		writeJSON(w, http.StatusOK, row)
	}
}

// ContinueVscreenIntervention creates a separate run after current native return verification.
func (h *Handler) ContinueVscreenIntervention(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) || requestUserID(r) == "" {
		writeVscreenReason(w, http.StatusForbidden, "permission_denied")
		return
	}
	row, rt, ok := h.loadVscreenIntervention(w, r)
	if !ok {
		return
	}
	var body struct {
		HumanSummary string `json:"human_summary"`
		FreshSession bool   `json:"fresh_session"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_request")
		return
	}
	actor, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user_id")
	if !ok {
		return
	}
	var observation protocol.VscreenQueryResult
	if row.State != "continued" {
		if h.DaemonHub == nil || !rt.DaemonID.Valid {
			writeVscreenReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
			return
		}
		var err error
		observation, err = h.DaemonHub.QueryVscreen(r.Context(), uuidToString(rt.WorkspaceID), uuidToString(rt.ID), rt.DaemonID.String, "state")
		if err != nil {
			writeVscreenError(w, err)
			return
		}
	}
	task, err := h.TaskService.ContinueAfterIntervention(r.Context(), service.InterventionContinuation{Intervention: row, ActorID: actor, HumanSummary: body.HumanSummary, FreshSession: body.FreshSession, Observation: observation})
	if err != nil {
		writeInterventionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		TaskID         string `json:"task_id"`
		InterventionID string `json:"intervention_id"`
	}{uuidToString(task.ID), uuidToString(row.ID)})
}

func writeInterventionError(w http.ResponseWriter, err error) {
	var rejection service.InterventionError
	var postgres *pgconn.PgError
	switch {
	case errors.As(err, &rejection):
		status := http.StatusConflict
		if rejection == "permission_denied" {
			status = http.StatusForbidden
		}
		if rejection == "invalid_summary" || rejection == "invalid_report" {
			status = http.StatusBadRequest
		}
		writeVscreenReason(w, status, string(rejection))
	case errors.Is(err, pgx.ErrNoRows):
		writeVscreenReason(w, http.StatusNotFound, "not_found")
	case errors.As(err, &postgres) && postgres.Code == "23505":
		writeVscreenReason(w, http.StatusConflict, "pending_conflict")
	default:
		writeVscreenReason(w, http.StatusInternalServerError, "intervention_failed")
	}
}

// CancelVscreenIntervention abandons a stopped handoff without replaying native actions.
func (h *Handler) CancelVscreenIntervention(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) || requestUserID(r) == "" {
		writeVscreenReason(w, http.StatusForbidden, "permission_denied")
		return
	}
	row, _, ok := h.loadVscreenIntervention(w, r)
	if !ok {
		return
	}
	actor, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user_id")
	if !ok {
		return
	}
	row, err := h.TaskService.CancelVscreenIntervention(r.Context(), row, actor)
	if err != nil {
		writeInterventionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}
