package handler

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type DocumentCommentResource struct {
	Kind              string `json:"kind"`
	ID                string `json:"id"`
	FileID            string `json:"fileId,omitempty"`
	ProjectID         string `json:"projectId,omitempty"`
	OwnerKind         string `json:"ownerKind,omitempty"`
	OwnerID           string `json:"ownerId,omitempty"`
	TaskID            string `json:"taskId,omitempty"`
	ProjectResourceID string `json:"projectResourceId,omitempty"`
	RuntimeID         string `json:"runtimeId,omitempty"`
	WorkDir           string `json:"workDir,omitempty"`
	RelativePath      string `json:"relativePath,omitempty"`
}

type documentCommentScope struct {
	userID      string
	workspaceID pgtype.UUID
}

func (h *Handler) documentCommentRequestScope(w http.ResponseWriter, r *http.Request) (documentCommentScope, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return documentCommentScope{}, false
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return documentCommentScope{}, false
	}
	return documentCommentScope{userID: userID, workspaceID: workspaceID}, true
}

// Resolve identity and access from live owners before reading or writing notes.
// A runtime may be offline: comments are server documents, not filesystem reads.
func (h *Handler) resolveDocumentCommentResource(w http.ResponseWriter, r *http.Request, scope documentCommentScope, input DocumentCommentResource) (DocumentCommentResource, string, bool) {
	switch input.Kind {
	case "issue":
		issue, ok := h.loadIssueForUser(w, r, input.ID)
		if !ok {
			return DocumentCommentResource{}, "", false
		}
		resource := DocumentCommentResource{Kind: "issue", ID: uuidToString(issue.ID)}
		return resource, "issue:" + resource.ID, true
	case "attachment":
		id, ok := parseUUIDOrBadRequest(w, input.ID, "attachment id")
		if !ok {
			return DocumentCommentResource{}, "", false
		}
		attachment, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{ID: id, WorkspaceID: scope.workspaceID})
		if err != nil {
			writeError(w, http.StatusNotFound, "attachment not found")
			return DocumentCommentResource{}, "", false
		}
		if !h.requireAttachmentVisible(w, r, uuidToString(scope.workspaceID), attachment) {
			return DocumentCommentResource{}, "", false
		}
		resource := DocumentCommentResource{Kind: "attachment", ID: uuidToString(attachment.ID)}
		return resource, "attachment:" + resource.ID, true
	case "workspace_file":
		return h.resolveFileCommentResource(w, r, scope, input)
	default:
		writeError(w, http.StatusBadRequest, "unsupported document resource")
		return DocumentCommentResource{}, "", false
	}
}

func (h *Handler) resolveFileCommentResource(w http.ResponseWriter, r *http.Request, scope documentCommentScope, input DocumentCommentResource) (DocumentCommentResource, string, bool) {
	relative := strings.ReplaceAll(input.RelativePath, "\\", "/")
	clean := path.Clean(relative)
	if (input.TaskID == "") == (input.ProjectResourceID == "") || len(relative) > 4096 || (input.FileID == "" && (relative == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.ContainsAny(clean, ":\x00"))) {
		writeError(w, http.StatusBadRequest, "a scoped relative document path is required")
		return DocumentCommentResource{}, "", false
	}
	resource := DocumentCommentResource{Kind: "workspace_file", RelativePath: clean}
	if input.ProjectResourceID != "" {
		id, ok := parseUUIDOrBadRequest(w, input.ProjectResourceID, "project_resource_id")
		if !ok {
			return DocumentCommentResource{}, "", false
		}
		owner, err := h.Queries.GetProjectResourceInWorkspace(r.Context(), db.GetProjectResourceInWorkspaceParams{ID: id, WorkspaceID: scope.workspaceID})
		if err != nil || owner.ResourceType != "local_directory" {
			writeError(w, http.StatusNotFound, "project directory not found")
			return DocumentCommentResource{}, "", false
		}
		if _, ok := h.loadProjectForResource(w, r, uuidToString(owner.ProjectID)); !ok {
			return DocumentCommentResource{}, "", false
		}
		var ref localDirectoryRef
		if json.Unmarshal(owner.ResourceRef, &ref) != nil || ref.LocalPath == "" || ref.DaemonID == "" || input.WorkDir != ref.LocalPath {
			writeError(w, http.StatusBadRequest, "path must match the project directory")
			return DocumentCommentResource{}, "", false
		}
		runtimes, err := h.Queries.ListAgentRuntimes(r.Context(), owner.WorkspaceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cannot resolve document runtime")
			return DocumentCommentResource{}, "", false
		}
		runtime := projectDirectoryRuntime(runtimes, ref.DaemonID, scope.userID)
		if runtime == nil || (input.RuntimeID != "" && input.RuntimeID != uuidToString(runtime.ID)) {
			writeError(w, http.StatusNotFound, "directory runtime unavailable")
			return DocumentCommentResource{}, "", false
		}
		resource.ProjectResourceID, resource.RuntimeID, resource.WorkDir, resource.ProjectID = uuidToString(owner.ID), uuidToString(runtime.ID), ref.LocalPath, uuidToString(owner.ProjectID)
		resource.OwnerKind, resource.OwnerID = "project_resource", resource.ProjectResourceID
	} else {
		id, ok := parseUUIDOrBadRequest(w, input.TaskID, "task_id")
		if !ok {
			return DocumentCommentResource{}, "", false
		}
		task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{ID: id, WorkspaceID: scope.workspaceID})
		if err != nil {
			writeError(w, http.StatusNotFound, "task not found")
			return DocumentCommentResource{}, "", false
		}
		runtime, _, ok := h.requireRuntimeReadAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, uuidToString(task.RuntimeID))
		if !ok {
			return DocumentCommentResource{}, "", false
		}
		if task.IssueID.Valid {
			issue, ok := h.loadIssueForUser(w, r, uuidToString(task.IssueID))
			if !ok {
				return DocumentCommentResource{}, "", false
			}
			resource.ProjectID = uuidToString(issue.ProjectID)
		} else if uuidToString(runtime.OwnerID) != scope.userID {
			writeError(w, http.StatusForbidden, "only the runtime owner can read this document")
			return DocumentCommentResource{}, "", false
		}
		if !task.WorkDir.Valid || task.WorkDir.String == "" || (input.WorkDir != "" && input.WorkDir != task.WorkDir.String) || (input.RuntimeID != "" && input.RuntimeID != uuidToString(runtime.ID)) {
			writeError(w, http.StatusBadRequest, "path must match the task directory")
			return DocumentCommentResource{}, "", false
		}
		resource.TaskID, resource.RuntimeID, resource.WorkDir = uuidToString(task.ID), uuidToString(runtime.ID), task.WorkDir.String
		resource, ok = h.bindWorkspaceFileTaskOwner(w, r, scope, task, resource)
		if !ok {
			return DocumentCommentResource{}, "", false
		}
	}
	return h.resolveRegisteredFileCommentResource(w, r, scope, resource, input.FileID)
}
