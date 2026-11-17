package handler

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) resolveRegisteredFileCommentResource(w http.ResponseWriter, r *http.Request, scope documentCommentScope, resource DocumentCommentResource, fileID string) (DocumentCommentResource, string, bool) {
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{ID: parseUUID(resource.RuntimeID), WorkspaceID: scope.workspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "document runtime unavailable")
		return DocumentCommentResource{}, "", false
	}
	directory := newWorkspaceFileDirectory(runtime, resource)
	var file db.WorkspaceFileResource
	if fileID != "" {
		id, ok := parseUUIDOrBadRequest(w, fileID, "fileId")
		if !ok {
			return DocumentCommentResource{}, "", false
		}
		file, err = h.Queries.GetWorkspaceFileResource(r.Context(), db.GetWorkspaceFileResourceParams{WorkspaceID: scope.workspaceID, DirectoryKey: directory.key, ID: id})
	} else {
		var files map[string]db.WorkspaceFileResource
		files, err = h.observeWorkspaceFiles(r.Context(), directory, []workspaceFileObservation{{Path: resource.RelativePath}}, false)
		if err == nil {
			var found bool
			file, found = files[resource.RelativePath]
			if !found {
				err = pgx.ErrNoRows
			}
		}
	}
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && file.Directory) {
		writeError(w, http.StatusNotFound, "document file not found")
		return DocumentCommentResource{}, "", false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot identify document file")
		return DocumentCommentResource{}, "", false
	}
	resource.FileID, resource.RelativePath = uuidToString(file.ID), file.RelativePath
	if err := h.Queries.AdoptLegacyWorkspaceFileComments(r.Context(), db.AdoptLegacyWorkspaceFileCommentsParams{
		FileID: file.ID, WorkspaceID: scope.workspaceID, WorkDir: resource.WorkDir, RelativePath: resource.RelativePath,
		RuntimeOwnerID: runtime.OwnerID, DaemonID: runtime.DaemonID.String, RuntimeID: runtime.ID, OwnerKind: resource.OwnerKind, OwnerID: resource.OwnerID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "cannot restore document discussions")
		return DocumentCommentResource{}, "", false
	}
	return resource, "workspace_file:" + resource.FileID, true
}
