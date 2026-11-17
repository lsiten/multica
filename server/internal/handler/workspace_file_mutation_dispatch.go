package handler

import (
	"context"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) admitWorkspaceFileMutation(ctx context.Context, mutation workspaceFileMutation) (db.WorkspaceFileOperation, error) {
	switch mutation.command.Action {
	case "file_operation":
		return h.admitWorkspaceFileOperation(ctx, mutation.directory, mutation.command)
	case "edit", "merge_edit":
		return h.admitWorkspaceFileEdit(ctx, mutation.directory, mutation.command)
	default:
		return db.WorkspaceFileOperation{}, protocol.ErrInvalidWorkspaceFileOperation
	}
}

func (h *Handler) completeWorkspaceFileMutation(ctx context.Context, mutation workspaceFileMutation, result protocol.LocalReviewResult) (protocol.LocalReviewResult, error) {
	switch mutation.command.Action {
	case "file_operation":
		return h.completeWorkspaceFileOperation(ctx, mutation, result)
	case "edit", "merge_edit":
		return h.completeWorkspaceFileEdit(ctx, mutation, result)
	default:
		return protocol.LocalReviewResult{}, protocol.ErrInvalidWorkspaceFileOperation
	}
}
