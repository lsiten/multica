package handler

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func adoptWorkspaceFileOperationDiscussions(ctx context.Context, q *db.Queries, directory workspaceFileDirectory, operation protocol.WorkspaceFileOperation) error {
	sources, err := q.ListLegacyWorkspaceFileSourcesOnHost(ctx, db.ListLegacyWorkspaceFileSourcesOnHostParams{WorkspaceID: directory.workspaceID, RuntimeOwnerID: directory.runtime.OwnerID, DaemonID: directory.runtime.DaemonID.String, RuntimeID: directory.runtime.ID})
	if err != nil {
		return err
	}
	for _, raw := range sources {
		var source DocumentCommentResource
		if err := json.Unmarshal(raw, &source); err != nil {
			return err
		}
		absolute := path.Join(path.Clean(strings.ReplaceAll(source.WorkDir, "\\", "/")), source.RelativePath)
		if !workspaceFileWithin(absolute, path.Join(directory.root, operation.Path)) && (operation.Kind != protocol.WorkspaceMove || !workspaceFileWithin(absolute, path.Join(directory.root, operation.Destination))) {
			continue
		}
		if source.Kind != "workspace_file" || !protocol.ValidWorkspaceOperationPath(source.RelativePath) || (source.TaskID == "") == (source.ProjectResourceID == "") {
			return errWorkspaceFileOwnerUnavailable
		}
		if source.TaskID != "" {
			id, err := util.ParseUUID(source.TaskID)
			if err != nil {
				return errWorkspaceFileOwnerUnavailable
			}
			task, err := q.GetAgentTaskInWorkspace(ctx, db.GetAgentTaskInWorkspaceParams{WorkspaceID: directory.workspaceID, ID: id})
			if err != nil {
				return err
			}
			source.OwnerKind, source.OwnerID, err = workspaceFileTaskOwner(task)
			if err != nil {
				return err
			}
		} else {
			id, err := util.ParseUUID(source.ProjectResourceID)
			if err != nil {
				return errWorkspaceFileOwnerUnavailable
			}
			source.ProjectResourceID = uuidToString(id)
			source.OwnerKind, source.OwnerID = "project_resource", source.ProjectResourceID
		}
		id, err := util.ParseUUID(source.RuntimeID)
		if err != nil {
			return errWorkspaceFileOwnerUnavailable
		}
		runtime, err := q.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{WorkspaceID: directory.workspaceID, ID: id})
		if err != nil {
			return err
		}
		files, err := observeWorkspaceFilesInTransaction(ctx, q, newWorkspaceFileDirectory(runtime, source), []workspaceFileObservation{{Path: source.RelativePath}}, false)
		if err != nil {
			return err
		}
		file := files[source.RelativePath]
		if err := q.AdoptLegacyWorkspaceFileComments(ctx, db.AdoptLegacyWorkspaceFileCommentsParams{FileID: file.ID, WorkspaceID: directory.workspaceID, WorkDir: source.WorkDir, RelativePath: source.RelativePath, RuntimeOwnerID: runtime.OwnerID, DaemonID: runtime.DaemonID.String, RuntimeID: runtime.ID, OwnerKind: source.OwnerKind, OwnerID: source.OwnerID}); err != nil {
			return err
		}
	}
	return nil
}
