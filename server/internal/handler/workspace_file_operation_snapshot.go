package handler

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workspaceFileSnapshot struct {
	ID           pgtype.UUID `json:"id"`
	DirectoryKey string      `json:"directory_key"`
	Root         string      `json:"root"`
	Path         string      `json:"path"`
	Directory    bool        `json:"directory"`
	State        string      `json:"state"`
	RecoveryID   pgtype.Text `json:"recovery_id"`
	Side         string      `json:"side"`
}

func workspaceFileWithin(file, root string) bool {
	return file == root || strings.HasPrefix(file, strings.TrimRight(root, "/")+"/")
}

func snapshotWorkspaceFiles(ctx context.Context, q *db.Queries, directory workspaceFileDirectory, operation protocol.WorkspaceFileOperation) ([]workspaceFileSnapshot, error) {
	snapshots := []workspaceFileSnapshot{}
	source := path.Join(directory.root, operation.Path)
	destination := path.Join(directory.root, operation.Destination)
	after := pgtype.UUID{Valid: true}
	for {
		rows, err := q.ListWorkspaceFileHostResources(ctx, db.ListWorkspaceFileHostResourcesParams{WorkspaceID: directory.workspaceID, RuntimeOwnerID: directory.runtime.OwnerID, DaemonID: directory.runtime.DaemonID.String, RuntimeID: directory.runtime.ID, AfterID: after})
		if err != nil {
			return nil, err
		}
		for _, file := range rows {
			after = file.ID
			var binding DocumentCommentResource
			if err := json.Unmarshal(file.BindingRef, &binding); err != nil {
				return nil, err
			}
			// Routing can disappear when a provider is removed. The persisted
			// namespace still proves which physical host owns the resource.
			if newWorkspaceFileDirectory(directory.runtime, binding).key != file.DirectoryKey {
				continue
			}
			root := path.Clean(strings.ReplaceAll(binding.WorkDir, "\\", "/"))
			absolute := path.Join(root, file.RelativePath)
			var side string
			switch operation.Kind {
			case protocol.WorkspaceMove, protocol.WorkspaceTrash:
				if workspaceFileWithin(absolute, source) {
					side = "source"
				} else if operation.Kind == protocol.WorkspaceMove && workspaceFileWithin(absolute, destination) {
					side = "destination"
				}
			case protocol.WorkspaceCreateFile, protocol.WorkspaceCreateDirectory, protocol.WorkspaceRestore:
				if workspaceFileWithin(absolute, source) {
					side = "destination"
				}
			default:
				return nil, protocol.ErrInvalidWorkspaceFileOperation
			}
			if side != "" {
				snapshots = append(snapshots, workspaceFileSnapshot{ID: file.ID, DirectoryKey: file.DirectoryKey, Root: root, Path: file.RelativePath, Directory: file.Directory, State: file.State, RecoveryID: file.RecoveryID, Side: side})
			}
		}
		if len(rows) < 500 {
			break
		}
	}
	if operation.Kind == protocol.WorkspaceRestore {
		original, err := q.GetWorkspaceFileOperationByCommand(ctx, db.GetWorkspaceFileOperationByCommandParams{WorkspaceID: directory.workspaceID, PhysicalKey: directory.physicalKey, CommandID: operation.RecoveryID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errWorkspaceFileRecoveryUnavailable
		}
		if err != nil {
			return nil, err
		}
		_, resources, err := workspaceFileRecoveryScope(original, directory)
		if err != nil {
			return nil, err
		}
		for _, resource := range resources {
			if resource.Side == "source" {
				resource.State = "trashed"
				resource.RecoveryID = pgtype.Text{String: operation.RecoveryID, Valid: true}
				snapshots = append(snapshots, resource)
			}
		}
	}
	return snapshots, nil
}
