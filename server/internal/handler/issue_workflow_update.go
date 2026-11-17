package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issueworkflow"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type IssueWorkflowWriteRequest struct {
	ExpectedUpdatedAt *string `json:"expected_updated_at,omitempty"`
	Name              *string `json:"name"`
	Description       *string `json:"description"`
	// Steps replace the workflow's steps and handoffs. Todo, done and
	// cancelled are added when missing; backlog and blocked are listed only
	// when included. Fixed statuses take their fixed places.
	Steps *[]IssueWorkflowStepInput `json:"steps"`
	// StatusMapping moves issues off statuses an edit removes, keyed by the
	// removed status. Required for every removed status that issues of the
	// workflow's projects still use.
	StatusMapping map[string]string `json:"status_mapping,omitempty"`
	// DryRun validates the edit and returns the mapping plan without writing.
	DryRun bool `json:"dry_run,omitempty"`
}

func (h *Handler) UpdateIssueWorkflow(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found")
	if !ok {
		return
	}
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow id")
	if !ok {
		return
	}
	var req IssueWorkflowWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var expected *time.Time
	if req.ExpectedUpdatedAt != nil {
		parsed, err := time.Parse(time.RFC3339Nano, *req.ExpectedUpdatedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid expected_updated_at")
			return
		}
		expected = &parsed
	}
	ctx := r.Context()

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	// The exclusive catalog lock keeps a status archive from racing the step
	// validation below, and serializes concurrent edits of one workspace's
	// workflows.
	if err := qtx.LockIssueStatusCatalog(ctx, wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	current, err := qtx.GetIssueWorkflow(ctx, db.GetIssueWorkflowParams{ID: idUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}
	if !canEditIssueWorkflow(member, current) {
		writeError(w, http.StatusForbidden, "only the workflow owner and workspace admins can change this workflow")
		return
	}
	if expected != nil && !current.UpdatedAt.Time.Equal(*expected) {
		writeErrorCode(w, http.StatusConflict, "workflow_conflict", "this workflow changed after editing started")
		return
	}
	var projects []db.Project
	if current.IsDefault {
		projects, err = qtx.ListProjectsUsingDefaultIssueWorkflow(ctx, wsUUID)
	} else {
		projects, err = qtx.ListProjectsUsingIssueWorkflow(ctx, db.ListProjectsUsingIssueWorkflowParams{WorkspaceID: wsUUID, WorkflowID: current.ID})
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	currentSteps, err := issueworkflow.DecodeSteps(current.Steps)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read workflow")
		return
	}
	name, description, steps := current.Name, current.Description, currentSteps
	if req.Name != nil && !current.IsDefault {
		name = *req.Name
	}
	if req.Description != nil {
		description = *req.Description
	}
	var createdStatuses []db.IssueStatus
	if req.Steps != nil {
		var msg string
		steps, createdStatuses, msg, err = resolveStepInputs(ctx, qtx, wsUUID, *req.Steps, isWorkspaceAdmin(member))
		if msg != "" {
			writeErrorCode(w, http.StatusBadRequest, "invalid_workflow", msg)
			return
		}
		if errors.Is(err, errStatusCreateForbidden) {
			writeErrorCode(w, http.StatusForbidden, "status_create_forbidden", err.Error())
			return
		}
		if err != nil {
			slog.Warn("UpdateIssueWorkflow statuses failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to save workflow")
			return
		}
	}
	name, description, steps = issueworkflow.Normalize(name, description, steps)
	catalog, err := h.workflowStatusCatalog(ctx, qtx, wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load issue statuses")
		return
	}
	if err := issueworkflow.Validate(name, description, steps, current.IsDefault, catalog); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid_workflow", err.Error())
		return
	}
	if status, msg := h.validateHandoffHandlers(ctx, r, workspaceID, steps, currentSteps); status != 0 {
		writeErrorCode(w, status, "invalid_handoff", msg)
		return
	}

	// Default accepts every status, so editing it never moves issues; issues
	// on a status it drops stay there and show under hidden columns.
	plan := workflowMappingPlan{Required: []StatusMappingRequirement{}, Unchanged: []StatusIssueCount{}}
	projectIDs := make([]pgtype.UUID, len(projects))
	for i, p := range projects {
		projectIDs[i] = p.ID
	}
	if !current.IsDefault && len(projectIDs) > 0 {
		next := issueworkflow.Definition{ID: current.ID, Name: name, Steps: steps}
		counts, err := qtx.CountProjectIssuesByStatus(ctx, db.CountProjectIssuesByStatusParams{WorkspaceID: wsUUID, ProjectIds: projectIDs})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save workflow")
			return
		}
		categoryOf := func(key string) string { return catalog[key].Category }
		plan = mappingPlan(counts, next.Has, func(key string) string { return issueworkflow.MoveTarget(next, key, categoryOf) })
		if req.DryRun {
			writeJSON(w, http.StatusOK, map[string]any{"dry_run": true, "plan": plan})
			return
		}
		if msg := checkMapping(plan, req.StatusMapping, next.Has); msg != "" {
			writeMappingRequired(w, plan, msg)
			return
		}
	} else if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"dry_run": true, "plan": plan})
		return
	}
	raw, err := issueworkflow.EncodeSteps(steps)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	row, err := qtx.UpdateIssueWorkflow(ctx, db.UpdateIssueWorkflowParams{
		ID:               current.ID,
		WorkspaceID:      wsUUID,
		Name:             name,
		Description:      description,
		InitialStatusKey: issueworkflow.StartStatus,
		Steps:            raw,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeErrorCode(w, http.StatusConflict, "workflow_name_taken", "a workflow with this name already exists")
			return
		}
		slog.Warn("UpdateIssueWorkflow failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	changed, cancelledWakeups, err := remapIssues(ctx, qtx, wsUUID, projectIDs, plan, req.StatusMapping)
	if err != nil {
		slog.Warn("UpdateIssueWorkflow remap failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to move issues to the new statuses")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	// Remapping is an administrative move: snapshots only, so it neither
	// notifies subscribers per issue nor looks like a handoff.
	h.publishIssueSnapshots(ctx, changed, "member", uuidToString(member.UserID))
	h.broadcastCancelledWakeups(ctx, wsUUID, cancelledWakeups)
	if len(createdStatuses) > 0 {
		h.publishIssueStatusChanged(workspaceID, member, "created")
	}
	h.publishIssueWorkflowChanged(workspaceID, member, "updated")
	h.writeIssueWorkflow(w, r, http.StatusOK, row)
}
