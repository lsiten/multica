package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func isUndefinedWorkspaceJevTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

type workspaceJevConfigResponse struct {
	WorkspaceID string                      `json:"workspace_id"`
	Config      protocol.WorkspaceJevConfig `json:"config"`
	Revision    int64                       `json:"revision"`
}
type updateWorkspaceJevConfigRequest struct {
	Config   protocol.WorkspaceJevConfig `json:"config"`
	Revision int64                       `json:"revision"`
}

func (h *Handler) GetWorkspaceJevConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(id), "workspace not found"); !ok {
		return
	}
	config, err := h.loadWorkspaceJevConfig(r.Context(), id)
	if err != nil {
		writeError(w, 500, "failed to load workspace Jev config")
		return
	}
	writeJSON(w, 200, workspaceJevConfigResponse{WorkspaceID: uuidToString(id), Config: config, Revision: config.Revision})
}

func (h *Handler) loadWorkspaceJevConfig(ctx context.Context, id pgtype.UUID) (protocol.WorkspaceJevConfig, error) {
	row, err := h.Queries.GetWorkspaceJevConfig(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return protocol.DefaultWorkspaceJevConfig(), nil
	}
	if isUndefinedWorkspaceJevTable(err) {
		return protocol.DefaultWorkspaceJevConfig(), nil
	}
	if err != nil {
		return protocol.WorkspaceJevConfig{}, err
	}
	var config protocol.WorkspaceJevConfig
	if json.Unmarshal(row.Config, &config) != nil || config.Validate() != nil {
		return config, errors.New("stored Jev configuration is invalid")
	}
	config.Revision = row.Revision
	return config, nil
}

func (h *Handler) UpdateWorkspaceJevConfig(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, 403, "Jev configuration requires a human administrator")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(id), "workspace not found", "owner", "admin"); !ok {
		return
	}
	var req *updateWorkspaceJevConfigRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || req == nil || decoder.Decode(new(any)) != io.EOF || req.Revision < 0 {
		writeError(w, 400, "invalid workspace Jev config")
		return
	}
	req.Config.Revision = 0
	if err := req.Config.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	payload, err := json.Marshal(req.Config)
	if err != nil {
		writeError(w, 400, "invalid workspace Jev config")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to save workspace Jev config")
		return
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)
	if _, err = q.LockWorkspaceForChatSessionCreate(r.Context(), id); err != nil {
		writeError(w, 404, "workspace not found")
		return
	}
	var revision int64
	if req.Revision == 0 {
		row, writeErr := q.CreateWorkspaceJevConfig(r.Context(), db.CreateWorkspaceJevConfigParams{WorkspaceID: id, Config: payload})
		err = writeErr
		revision = row.Revision
	} else {
		row, writeErr := q.UpdateWorkspaceJevConfig(r.Context(), db.UpdateWorkspaceJevConfigParams{WorkspaceID: id, Config: payload, Revision: req.Revision})
		err = writeErr
		revision = row.Revision
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "workspace Jev config changed; reload and retry")
		return
	}
	if isUndefinedWorkspaceJevTable(err) {
		writeError(w, http.StatusServiceUnavailable, "workspace Jev migration is not installed; run database migrations and retry")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to save workspace Jev config")
		return
	}
	if tx.Commit(r.Context()) != nil {
		writeError(w, 500, "failed to commit workspace Jev config")
		return
	}
	req.Config.Revision = revision
	writeJSON(w, 200, workspaceJevConfigResponse{WorkspaceID: uuidToString(id), Config: req.Config, Revision: revision})
}

func (h *Handler) captureWorkspaceJevConfig(ctx context.Context, task db.AgentTaskQueue, workspaceID pgtype.UUID) (protocol.WorkspaceJevConfig, error) {
	var saved map[string]json.RawMessage
	if len(task.Context) > 0 && json.Unmarshal(task.Context, &saved) != nil {
		return protocol.WorkspaceJevConfig{}, errors.New("invalid task context")
	}
	if raw, exists := saved["workspace_jev"]; exists {
		return decodeJevSnapshot(raw)
	}
	config, err := h.loadWorkspaceJevConfig(ctx, workspaceID)
	if err != nil {
		return config, err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return config, err
	}
	// Return the winning database snapshot, including on concurrent redelivery.
	contextBytes, err := h.Queries.CaptureTaskJevConfig(ctx, db.CaptureTaskJevConfigParams{TaskID: task.ID, WorkspaceID: workspaceID, Config: raw})
	if err != nil {
		return config, fmt.Errorf("persist Jev snapshot: %w", err)
	}
	if json.Unmarshal(contextBytes, &saved) != nil {
		return config, errors.New("invalid persisted task context")
	}
	return decodeJevSnapshot(saved["workspace_jev"])
}

func decodeJevSnapshot(raw json.RawMessage) (protocol.WorkspaceJevConfig, error) {
	var config protocol.WorkspaceJevConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return config, err
	}
	return config, config.Validate()
}
