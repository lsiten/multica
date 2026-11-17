package handler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errWorkspaceFileCommandMismatch = errors.New("file operation ID belongs to another request")
var errWorkspaceFileRecoveryUnavailable = errors.New("the original deletion has not been confirmed")

func (h *Handler) admitWorkspaceFileOperation(ctx context.Context, directory workspaceFileDirectory, command protocol.LocalReviewCommand) (db.WorkspaceFileOperation, error) {
	if command.FileOperation == nil || command.FileOperation.Validate() != nil || command.CommandID == "" {
		return db.WorkspaceFileOperation{}, protocol.ErrInvalidWorkspaceFileOperation
	}
	operation := *command.FileOperation
	payload, err := json.Marshal(operation)
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
	if operation.ExpectedFileID != "" {
		id, err := util.ParseUUID(operation.ExpectedFileID)
		if err != nil {
			return db.WorkspaceFileOperation{}, protocol.ErrInvalidWorkspaceFileOperation
		}
		file, err := q.GetWorkspaceFileResource(ctx, db.GetWorkspaceFileResourceParams{WorkspaceID: directory.workspaceID, DirectoryKey: directory.key, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.WorkspaceFileOperation{}, errWorkspaceFileUnavailable
		}
		if err != nil {
			return db.WorkspaceFileOperation{}, err
		}
		if file.State != "active" || file.RelativePath != operation.Path {
			return db.WorkspaceFileOperation{}, errWorkspaceFileUnavailable
		}
	}
	if err := adoptWorkspaceFileOperationDiscussions(ctx, q, directory, operation); err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	if operation.Kind == protocol.WorkspaceMove || operation.Kind == protocol.WorkspaceTrash {
		if _, err := observeWorkspaceFilesInTransaction(ctx, q, directory, []workspaceFileObservation{{Path: operation.Path}}, false); err != nil {
			return db.WorkspaceFileOperation{}, err
		}
	}
	resources, err := snapshotWorkspaceFiles(ctx, q, directory, operation)
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	snapshot, err := json.Marshal(resources)
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	operation.Content = ""
	payload, err = json.Marshal(operation)
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	binding, err := json.Marshal(directory.binding)
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	admitted, err := q.CreateWorkspaceFileOperation(ctx, db.CreateWorkspaceFileOperationParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey, PhysicalKey: directory.physicalKey, DirectoryKey: directory.key, RootPath: directory.root, CommandID: command.CommandID, ActorID: parseUUID(command.ActorID), RequestHash: fingerprint, Operation: payload, BindingRef: binding, Resources: snapshot})
	if err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	if _, err := q.BumpWorkspaceFileHostRevision(ctx, db.BumpWorkspaceFileHostRevisionParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey}); err != nil {
		return db.WorkspaceFileOperation{}, err
	}
	return admitted, tx.Commit(ctx)
}
