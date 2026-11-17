package handler

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) filterWorkspaceTrash(ctx context.Context, scope workspaceFileMutation, result *protocol.LocalReviewResult) error {
	var page protocol.WorkspaceTrashPage
	limit := scope.command.Limit
	if limit == 0 {
		limit = 100
	}
	if result.EffectsUnknown || result.FileOperationRejected || len(result.Edit) != 0 || json.Unmarshal(result.Page, &page) != nil || page.Kind != "workspace_trash" || page.Path != scope.command.Path || len(page.Entries) > limit || page.NextOffset < scope.command.Offset || page.NextOffset-scope.command.Offset > limit || (page.HasMore && page.NextOffset == scope.command.Offset) {
		return errInvalidWorkspaceFileObservation
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	directory := scope.directory
	host, err := lockWorkspaceFileHost(ctx, q, directory)
	if err != nil {
		return err
	}
	if err := checkWorkspaceFileRead(ctx, q, directory, host.Revision); err != nil {
		return err
	}
	if err := lockDocumentCommentOwner(ctx, q, directory.workspaceID, directory.binding); err != nil {
		return err
	}
	visible := make([]protocol.WorkspaceTrashEntry, 0, len(page.Entries))
	for _, entry := range page.Entries {
		original, err := q.GetWorkspaceFileOperationByCommand(ctx, db.GetWorkspaceFileOperationByCommandParams{WorkspaceID: directory.workspaceID, PhysicalKey: directory.physicalKey, CommandID: entry.RecoveryID})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		deleted, _, err := workspaceFileRecoveryScope(original, directory)
		if errors.Is(err, errWorkspaceFileRecoveryUnavailable) {
			continue
		}
		if err != nil {
			return err
		}
		confirmed, err := completedWorkspaceFileResult(original)
		if err != nil {
			return err
		}
		receipt, err := protocol.ParseWorkspaceFileReceipt(protocol.LocalReviewCommand{Action: "file_operation", CommandID: original.CommandID, FileOperationID: uuidToString(original.ID), FileOperation: &deleted}, confirmed)
		if err != nil {
			return err
		}
		item := protocol.WorkspaceTrashEntry{RecoveryID: original.CommandID, Path: deleted.Path, Directory: receipt.Directory, ActorID: uuidToString(original.ActorID)}
		if original.CompletedAt.Valid {
			item.DeletedAt = original.CompletedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		visible = append(visible, item)
	}
	page.Entries = visible
	encoded, err := json.Marshal(page)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	result.Page = encoded
	return nil
}
