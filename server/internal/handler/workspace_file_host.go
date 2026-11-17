package handler

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"path"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var errWorkspaceFileDirectoryBusy = errors.New("a file operation in this directory is unconfirmed")
var errWorkspaceFileDirectoryChanged = errors.New("directory changed while its files were being read")
var errWorkspaceFileUnavailable = errors.New("this file identity is no longer available at this source")
var errWorkspaceFileOperationUnknown = errors.New("the file operation result is unknown")

func lockWorkspaceFileHost(ctx context.Context, q *db.Queries, directory workspaceFileDirectory) (db.WorkspaceFileHostState, error) {
	// Workspace teardown locks the workspace before deleting coordination rows.
	if _, err := q.LockWorkspaceForChatSessionCreate(ctx, directory.workspaceID); err != nil {
		return db.WorkspaceFileHostState{}, err
	}
	if err := q.EnsureWorkspaceFileHost(ctx, db.EnsureWorkspaceFileHostParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey}); err != nil {
		return db.WorkspaceFileHostState{}, err
	}
	return q.LockWorkspaceFileHost(ctx, db.LockWorkspaceFileHostParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey})
}

func checkWorkspaceFileRead(ctx context.Context, q *db.Queries, directory workspaceFileDirectory, revision int64) error {
	if directory.readRevision != nil && *directory.readRevision != revision {
		return errWorkspaceFileDirectoryChanged
	}
	busy, err := q.WorkspaceFileOperationOverlaps(ctx, db.WorkspaceFileOperationOverlapsParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey, RootPath: directory.root})
	if err != nil {
		return err
	}
	if busy {
		return errWorkspaceFileDirectoryBusy
	}
	return nil
}

func (h *Handler) captureWorkspaceFileRead(ctx context.Context, directory workspaceFileDirectory) (workspaceFileDirectory, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return directory, err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	host, err := lockWorkspaceFileHost(ctx, q, directory)
	if err != nil {
		return directory, err
	}
	if err := checkWorkspaceFileRead(ctx, q, directory, host.Revision); err != nil {
		return directory, err
	}
	if directory.expectedFileID != "" {
		file, err := q.GetWorkspaceFileResource(ctx, db.GetWorkspaceFileResourceParams{WorkspaceID: directory.workspaceID, DirectoryKey: directory.key, ID: parseUUID(directory.expectedFileID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return directory, errWorkspaceFileUnavailable
		}
		if err != nil {
			return directory, err
		}
		if file.State != "active" || file.Directory {
			return directory, errWorkspaceFileUnavailable
		}
		directory.binding.RelativePath = file.RelativePath
	} else if directory.binding.RelativePath != "" {
		directory.identityAmbiguous, err = q.WorkspaceFilePathWasPublished(ctx, db.WorkspaceFilePathWasPublishedParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey, AbsolutePath: path.Join(directory.root, directory.binding.RelativePath)})
		if err != nil {
			return directory, err
		}
	}
	directory.readRevision = &host.Revision
	return directory, tx.Commit(ctx)
}
