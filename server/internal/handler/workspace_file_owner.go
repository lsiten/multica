package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errWorkspaceFileOwnerUnavailable = errors.New("task directory ownership is unavailable")

func workspaceFileTaskOwner(task db.AgentTaskQueue) (string, string, error) {
	if _, coordination := service.ProjectCoordination(task); coordination {
		return "", "", errWorkspaceFileOwnerUnavailable
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(task.Result, &result) == nil {
		if raw, present := result["code_workspace"]; present {
			var selection protocol.CodeWorkspaceSelection
			if json.Unmarshal(raw, &selection) != nil || task.ChatSessionID.Valid || !task.IssueID.Valid || selection.WorkDir != task.WorkDir.String || selection.RuntimeID != uuidToString(task.RuntimeID) {
				return "", "", errWorkspaceFileOwnerUnavailable
			}
			owner, err := util.ParseUUID(selection.IssueID)
			if err != nil {
				return "", "", errWorkspaceFileOwnerUnavailable
			}
			return "issue", uuidToString(owner), nil
		}
	}
	if task.ChatSessionID.Valid {
		return "chat", uuidToString(task.ChatSessionID), nil
	}
	if task.IssueID.Valid {
		return "issue", uuidToString(task.IssueID), nil
	}
	return "task", uuidToString(task.ID), nil
}

// Resource ownership survives task generations and project transfers. Access
// still follows the current owner, including a shared checkout's root issue.
func (h *Handler) bindWorkspaceFileTaskOwner(w http.ResponseWriter, r *http.Request, scope documentCommentScope, task db.AgentTaskQueue, resource DocumentCommentResource) (DocumentCommentResource, bool) {
	if _, coordination := service.ProjectCoordination(task); coordination {
		writeError(w, http.StatusForbidden, "project supervision directories are not workspace files")
		return DocumentCommentResource{}, false
	}
	kind, id, err := workspaceFileTaskOwner(task)
	if err != nil {
		writeError(w, http.StatusConflict, errWorkspaceFileOwnerUnavailable.Error())
		return DocumentCommentResource{}, false
	}
	if task.ChatSessionID.Valid {
		session, ok := h.gatePublicChatSessionForUser(w, r, scope.userID, uuidToString(scope.workspaceID), uuidToString(task.ChatSessionID))
		if !ok {
			return DocumentCommentResource{}, false
		}
		resource.OwnerKind, resource.OwnerID, resource.ProjectID = "chat", uuidToString(session.ID), uuidToString(session.ProjectID)
		return resource, true
	}
	if task.IssueID.Valid {
		if id != uuidToString(task.IssueID) {
			issue, ok := h.loadIssueForUser(w, r, id)
			if !ok {
				return DocumentCommentResource{}, false
			}
			resource.ProjectID = uuidToString(issue.ProjectID)
		}
		resource.OwnerKind, resource.OwnerID = kind, id
		return resource, true
	}
	resource.OwnerKind, resource.OwnerID = kind, id
	return resource, true
}
