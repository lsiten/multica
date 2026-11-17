package handler

import (
	"context"
	"encoding/json"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) completeWorkspaceFileEdit(ctx context.Context, mutation workspaceFileMutation, result protocol.LocalReviewResult) (protocol.LocalReviewResult, error) {
	rejected := result.Error != "" && result.FileOperationRejected && !result.EffectsUnknown && len(result.Page) == 0 && len(result.Edit) == 0
	if !rejected {
		if _, err := protocol.ParseWorkspaceFileEditReceipt(mutation.command, result); err != nil {
			return protocol.LocalReviewResult{}, err
		}
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	directory := mutation.directory
	if _, err := lockWorkspaceFileHost(ctx, q, directory); err != nil {
		return protocol.LocalReviewResult{}, err
	}
	record, err := q.GetWorkspaceFileOperation(ctx, db.GetWorkspaceFileOperationParams{WorkspaceID: directory.workspaceID, ID: mutation.admission.ID})
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	if record.State != "pending" {
		return completedWorkspaceFileResult(record)
	}
	if mutation.requiresUnsent && record.DispatchStarted {
		return protocol.LocalReviewResult{}, errWorkspaceFileDirectoryBusy
	}
	if !mutation.requiresUnsent && !record.DispatchStarted {
		return protocol.LocalReviewResult{}, errWorkspaceFileSnapshotChanged
	}
	if _, err := q.BumpWorkspaceFileHostRevision(ctx, db.BumpWorkspaceFileHostRevisionParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey}); err != nil {
		return protocol.LocalReviewResult{}, err
	}
	result.ClaimToken = ""
	payload, err := json.Marshal(result)
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	state := "completed"
	if rejected {
		state = "rejected"
	}
	updated, err := q.FinishWorkspaceFileOperation(ctx, db.FinishWorkspaceFileOperationParams{WorkspaceID: directory.workspaceID, ID: record.ID, State: state, Result: payload})
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	if updated != 1 {
		return protocol.LocalReviewResult{}, errWorkspaceFileSnapshotChanged
	}
	if err := tx.Commit(ctx); err != nil {
		return protocol.LocalReviewResult{}, err
	}
	return result, nil
}
