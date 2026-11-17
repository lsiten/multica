package handler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workspaceFileEditIntent struct {
	Kind   string `json:"kind"`
	FileID string `json:"file_id"`
	Path   string `json:"path"`
}

func (h *Handler) admitWorkspaceFileEdit(ctx context.Context, directory workspaceFileDirectory, command protocol.LocalReviewCommand) (db.WorkspaceFileOperation, error) {
	if command.CommandID == "" || len(command.CommandID) > 128 || command.FileID == "" || (command.Action != "edit" && command.Action != "merge_edit") {
		return db.WorkspaceFileOperation{}, protocol.ErrInvalidWorkspaceFileOperation
	}
	payload, err := json.Marshal(struct {
		Action, FileID, BaseSHA, Base, New string
	}{command.Action, command.FileID, command.EditBaseSHA, command.EditBase, command.EditNew})
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	fingerprint := workspaceFileDigest(string(payload))
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	host, err := lockWorkspaceFileHost(ctx, q, directory)
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	previous, err := q.GetWorkspaceFileOperationByCommand(ctx, db.GetWorkspaceFileOperationByCommandParams{WorkspaceID: directory.workspaceID, PhysicalKey: directory.physicalKey, CommandID: command.CommandID})
	if err == nil {
		if previous.DirectoryKey != directory.key || previous.ActorID != parseUUID(command.ActorID) || previous.RequestHash != fingerprint {
			return db.WorkspaceFileOperation{}, errWorkspaceFileCommandMismatch
		}
		return previous, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.WorkspaceFileOperation{}, err
	}
	if err := checkWorkspaceFileRead(ctx, q, directory, host.Revision); err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	if err := lockDocumentCommentOwner(ctx, q, directory.workspaceID, directory.binding); err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	file, err := q.GetWorkspaceFileResource(ctx, db.GetWorkspaceFileResourceParams{WorkspaceID: directory.workspaceID, DirectoryKey: directory.key, ID: parseUUID(command.FileID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WorkspaceFileOperation{}, errWorkspaceFileUnavailable
	}
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	if file.State != "active" || file.Directory {
		return db.WorkspaceFileOperation{}, errWorkspaceFileUnavailable
	}
	payload, err = json.Marshal(workspaceFileEditIntent{Kind: command.Action, FileID: command.FileID, Path: file.RelativePath})
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	directory.binding.RelativePath = file.RelativePath
	binding, err := json.Marshal(directory.binding)
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	admitted, err := q.CreateWorkspaceFileOperation(ctx, db.CreateWorkspaceFileOperationParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey, PhysicalKey: directory.physicalKey, DirectoryKey: directory.key, RootPath: directory.root, CommandID: command.CommandID, ActorID: parseUUID(command.ActorID), RequestHash: fingerprint, Operation: payload, BindingRef: binding, Resources: []byte("[]")})
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	if _, err := q.BumpWorkspaceFileHostRevision(ctx, db.BumpWorkspaceFileHostRevisionParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey}); err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	return admitted, tx.Commit(ctx)
}
