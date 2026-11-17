package handler

import (
	"context"
	"encoding/json"
	"net/http"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) relayWorkspaceFileCommand(ctx context.Context, command protocol.LocalReviewCommand) (protocol.LocalReviewResult, error) {
	exchange, err := h.localReviewRelay.enqueue(command)
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	return h.localReviewRelay.wait(ctx, exchange)
}

func (h *Handler) markWorkspaceFileDispatch(ctx context.Context, mutation workspaceFileMutation) (db.WorkspaceFileOperation, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	if _, err := lockWorkspaceFileHost(ctx, q, mutation.directory); err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	record, err := q.GetWorkspaceFileOperation(ctx, db.GetWorkspaceFileOperationParams{WorkspaceID: mutation.directory.workspaceID, ID: mutation.admission.ID})
	if err != nil {
		return record, err
	}
	if record.State == "pending" {
		record, err = q.MarkWorkspaceFileOperationDispatched(ctx, db.MarkWorkspaceFileOperationDispatchedParams{WorkspaceID: mutation.directory.workspaceID, ID: mutation.admission.ID})
		if err != nil {
			return record, err
		}
	}
	return record, tx.Commit(ctx)
}

func writeWorkspaceFileOperationResult(w http.ResponseWriter, result protocol.LocalReviewResult) {
	if result.Error != "" {
		if result.FileOperationRejected && !result.EffectsUnknown {
			writeErrorCode(w, http.StatusConflict, "workspace_file_operation_rejected", result.Error)
		} else {
			writeError(w, http.StatusConflict, result.Error)
		}
		return
	}
	result.ClaimToken = ""
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) forwardWorkspaceFileOperation(w http.ResponseWriter, r *http.Request, mutation workspaceFileMutation) {
	record, err := h.admitWorkspaceFileMutation(r.Context(), mutation)
	if err != nil {
		writeWorkspaceFileCoordinationError(w, err)
		return
	}
	mutation.admission = record
	mutation.command.FileOperationID = uuidToString(record.ID)
	if mutation.command.Action == "edit" || mutation.command.Action == "merge_edit" {
		var intent workspaceFileEditIntent
		if json.Unmarshal(record.Operation, &intent) != nil || intent.FileID != mutation.command.FileID || intent.Kind != mutation.command.Action {
			writeWorkspaceFileCoordinationError(w, errWorkspaceFileCommandMismatch)
			return
		}
		mutation.command.RelativePath = intent.Path
	}
	if record.State != "pending" {
		result, err := completedWorkspaceFileResult(record)
		if err != nil {
			writeWorkspaceFileCoordinationError(w, err)
			return
		}
		writeWorkspaceFileOperationResult(w, result)
		return
	}
	probe := mutation.command
	probe.Action, probe.CommandID, probe.FileOperationID, probe.FileOperation = "workspace_files", "", "", nil
	probe.FileID, probe.EditBase, probe.EditNew, probe.EditBaseSHA = "", "", "", ""
	probe.RelativePath, probe.Offset, probe.Limit = ".", 0, 1
	recursive := false
	probe.Recursive = &recursive
	capability, readErr := h.relayWorkspaceFileCommand(r.Context(), probe)
	var supported struct {
		Path          string `json:"path"`
		Supported     bool   `json:"file_operation_identity_supported"`
		EditSupported bool   `json:"file_edit_identity_supported"`
	}
	decoded := json.Unmarshal(capability.Page, &supported) == nil
	identitySupported := supported.Supported
	if mutation.command.Action != "file_operation" {
		identitySupported = supported.EditSupported
	}
	if readErr != nil || capability.Error != "" || !decoded || supported.Path != mutation.command.Path || !identitySupported {
		mutation.requiresUnsent = true
		result, err := h.completeWorkspaceFileMutation(r.Context(), mutation, protocol.LocalReviewResult{Error: "runtime cannot confirm file operation identities", FileOperationRejected: true})
		if err != nil {
			writeWorkspaceFileCoordinationError(w, err)
			return
		}
		writeWorkspaceFileOperationResult(w, result)
		return
	}
	record, err = h.markWorkspaceFileDispatch(r.Context(), mutation)
	if err != nil {
		writeWorkspaceFileCoordinationError(w, err)
		return
	}
	if record.State != "pending" {
		result, err := completedWorkspaceFileResult(record)
		if err != nil {
			writeWorkspaceFileCoordinationError(w, err)
			return
		}
		writeWorkspaceFileOperationResult(w, result)
		return
	}
	physical, err := h.relayWorkspaceFileCommand(r.Context(), mutation.command)
	if err != nil {
		writeWorkspaceFileCoordinationError(w, err)
		return
	}
	result, err := h.completeWorkspaceFileMutation(r.Context(), mutation, physical)
	if err != nil {
		if physical.Error != "" {
			writeError(w, http.StatusConflict, physical.Error)
		} else {
			writeWorkspaceFileCoordinationError(w, err)
		}
		return
	}
	writeWorkspaceFileOperationResult(w, result)
}
