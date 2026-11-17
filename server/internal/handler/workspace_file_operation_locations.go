package handler

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errWorkspaceFileSnapshotChanged = errors.New("file identities changed after the operation was admitted")

func applyWorkspaceFileLocations(ctx context.Context, q *db.Queries, mutation workspaceFileMutation, resources []workspaceFileSnapshot, receipt protocol.WorkspaceFileOperationResult) error {
	directory, operation := mutation.directory, mutation.command.FileOperation
	source := path.Join(directory.root, operation.Path)
	target := path.Join(directory.root, operation.Destination)
	if operation.Kind == protocol.WorkspaceRestore {
		original, err := q.GetWorkspaceFileOperationByCommand(ctx, db.GetWorkspaceFileOperationByCommandParams{WorkspaceID: directory.workspaceID, PhysicalKey: directory.physicalKey, CommandID: operation.RecoveryID})
		if err != nil {
			return err
		}
		var deleted protocol.WorkspaceFileOperation
		if err := unmarshalWorkspaceFileOperation(original.Operation, &deleted); err != nil {
			return err
		}
		source, target = path.Join(original.RootPath, deleted.Path), path.Join(directory.root, operation.Path)
	}
	// Retire stale destination slots before publishing the moved identities.
	for _, side := range []string{"destination", "source"} {
		for _, resource := range resources {
			if resource.Side != side {
				continue
			}
			nextPath, state, recovery, isDirectory := resource.Path, "retired", pgtype.Text{}, resource.Directory
			if side == "source" {
				absolute := path.Join(resource.Root, resource.Path)
				if absolute == source {
					isDirectory = receipt.Directory
				}
				switch operation.Kind {
				case protocol.WorkspaceTrash:
					state, recovery = "trashed", pgtype.Text{String: mutation.command.CommandID, Valid: true}
				case protocol.WorkspaceMove, protocol.WorkspaceRestore:
					moved := target + strings.TrimPrefix(absolute, source)
					if moved != resource.Root && workspaceFileWithin(moved, resource.Root) {
						nextPath, state = strings.TrimPrefix(moved, strings.TrimRight(resource.Root, "/")+"/"), "active"
					} else {
						state = "detached"
					}
				default:
					return protocol.ErrInvalidWorkspaceFileOperation
				}
			}
			updated, err := q.RelocateWorkspaceFileResource(ctx, db.RelocateWorkspaceFileResourceParams{WorkspaceID: directory.workspaceID, ID: resource.ID, PreviousState: resource.State, PreviousPath: resource.Path, PreviousRecoveryID: resource.RecoveryID, NewPath: nextPath, NewPathKey: workspaceFileDigest(nextPath), NewState: state, NewRecoveryID: recovery, Directory: isDirectory})
			if err != nil {
				return err
			}
			if updated == 0 {
				_, err := q.GetWorkspaceFileResourceByID(ctx, db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: resource.ID})
				if !errors.Is(err, pgx.ErrNoRows) {
					return errWorkspaceFileSnapshotChanged
				}
			}
		}
	}
	return nil
}
