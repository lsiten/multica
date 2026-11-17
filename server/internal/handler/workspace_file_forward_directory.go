package handler

import (
	"context"
	"errors"
	"net/http"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) prepareWorkspaceFileDirectory(ctx context.Context, command protocol.LocalReviewCommand, binding DocumentCommentResource) (workspaceFileDirectory, error) {
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: parseUUID(command.RuntimeID), WorkspaceID: parseUUID(command.WorkspaceID)})
	if err != nil {
		return workspaceFileDirectory{}, err
	}
	if command.Action == "file_content" {
		binding.RelativePath = command.RelativePath
	} else {
		binding.RelativePath = ""
	}
	directory := newWorkspaceFileDirectory(runtime, binding)
	directory.expectedFileID = command.FileID
	if command.Action == "workspace_files" || command.Action == "file_content" || command.Action == "workspace_trash" {
		return h.captureWorkspaceFileRead(ctx, directory)
	}
	return directory, nil
}

func writeWorkspaceFileCoordinationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errWorkspaceFileDirectoryBusy):
		writeErrorCode(w, http.StatusConflict, "workspace_file_directory_busy", errWorkspaceFileDirectoryBusy.Error())
	case errors.Is(err, errWorkspaceFileDirectoryChanged):
		writeErrorCode(w, http.StatusConflict, "workspace_file_directory_changed", errWorkspaceFileDirectoryChanged.Error())
	case errors.Is(err, errWorkspaceFileCommandMismatch):
		writeError(w, http.StatusConflict, errWorkspaceFileCommandMismatch.Error())
	case errors.Is(err, errWorkspaceFileRecoveryUnavailable):
		writeError(w, http.StatusConflict, errWorkspaceFileRecoveryUnavailable.Error())
	case errors.Is(err, errWorkspaceFileUnavailable):
		writeErrorCode(w, http.StatusConflict, "workspace_file_unavailable", errWorkspaceFileUnavailable.Error())
	case errors.Is(err, errWorkspaceFileOperationUnknown):
		writeErrorCode(w, http.StatusConflict, "workspace_file_operation_unknown", errWorkspaceFileOperationUnknown.Error())
	default:
		writeError(w, http.StatusBadGateway, "workspace file operation is unconfirmed")
	}
}
