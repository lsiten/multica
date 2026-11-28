package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ForwardLocalReview authorizes the directory. File mutations additionally
// preserve their admission, identity snapshot and terminal response together.
func (h *Handler) ForwardLocalReview(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "human actor required")
		return
	}
	var input struct {
		FileOperation     *protocol.WorkspaceFileOperation `json:"file_operation,omitempty"`
		FileID            string                           `json:"file_id,omitempty"`
		IndexID           string                           `json:"index_id,omitempty"`
		Branch            string                           `json:"branch,omitempty"`
		Head              string                           `json:"head,omitempty"`
		Message           string                           `json:"message,omitempty"`
		Paths             []string                         `json:"paths,omitempty"`
		TaskID            string                           `json:"task_id"`
		ProjectResourceID string                           `json:"project_resource_id,omitempty"`
		GitScope          string                           `json:"git_scope,omitempty"`
		CommitSHA         string                           `json:"commit_sha,omitempty"`
		Scope             string                           `json:"scope,omitempty"`
		RuntimeID         string                           `json:"runtime_id,omitempty"`
		Path              string                           `json:"path"`
		Target            string                           `json:"target"`
		Action            string                           `json:"action"`
		SnapshotID        string                           `json:"snapshot_id"`
		VersionID         string                           `json:"version_id,omitempty"`
		FilePath          string                           `json:"file_path,omitempty"`
		RelativePath      string                           `json:"relative_path,omitempty"`
		Recursive         *bool                            `json:"recursive,omitempty"`
		Side              string                           `json:"side,omitempty"`
		Offset            int                              `json:"offset,omitempty"`
		Limit             int                              `json:"limit,omitempty"`
		Comment           string                           `json:"comment"`
		CommandID         string                           `json:"command_id"`
		EditBase          string                           `json:"edit_base,omitempty"`
		EditNew           string                           `json:"edit_new,omitempty"`
		EditBaseSHA       string                           `json:"edit_base_sha,omitempty"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&input) != nil {
		writeError(w, http.StatusBadRequest, "invalid review request")
		return
	}
	if protocol.IsRetiredLocalReviewAction(input.Action) {
		writeError(w, http.StatusGone, protocol.LocalReviewWritesRetired)
		return
	}
	if (input.Action != "workspace_trash" && input.Action != "file_operation" && input.Action != "lease" && input.Action != "index" && input.Action != "branches" && input.Action != "repositories" && input.Action != "versions" && input.Action != "workspace_files" && input.Action != "file_content" && input.Scope != "checkout" && strings.TrimSpace(input.Target) == "") || len(input.Target) > 250 {
		writeError(w, http.StatusBadRequest, "task and target branch required")
		return
	}
	if (input.TaskID == "") == (input.ProjectResourceID == "") {
		writeError(w, http.StatusBadRequest, "exactly one task or project resource is required")
		return
	}
	if input.Scope != "" && input.Scope != "run" && input.Scope != "directory" && input.Scope != "version" && input.Scope != "checkout" {
		writeError(w, http.StatusBadRequest, "invalid review scope")
		return
	}
	if input.Scope == "checkout" && !protocol.IsCheckoutRead(input.Action) {
		writeError(w, http.StatusBadRequest, "checkout scope is read only")
		return
	}
	if input.Scope == "run" && input.TaskID == "" {
		writeError(w, http.StatusBadRequest, "run review scope requires a task")
		return
	}
	if input.Scope == "directory" && input.ProjectResourceID == "" {
		writeError(w, http.StatusBadRequest, "directory review scope requires a project resource")
		return
	}
	if input.Scope == "version" && input.VersionID == "" {
		writeError(w, http.StatusBadRequest, "version review scope requires a version")
		return
	}
	if input.FileID != "" {
		if input.Action != "file_content" && input.Action != "edit" && input.Action != "merge_edit" {
			writeError(w, http.StatusBadRequest, "file identity is only supported for file reads and edits")
			return
		}
		id, ok := parseUUIDOrBadRequest(w, input.FileID, "file_id")
		if !ok {
			return
		}
		input.FileID = uuidToString(id)
	}
	identityEdit := input.FileID != "" && (input.Action == "edit" || input.Action == "merge_edit")
	if input.ProjectResourceID != "" && !protocol.IsProjectDirectoryRead(input.Action) {
		writeError(w, http.StatusForbidden, "project directories are read only")
		return
	}
	switch input.Action {
	case "file_operation":
		if input.FileOperation == nil || input.FileOperation.Validate() != nil || strings.TrimSpace(input.CommandID) == "" || len(input.CommandID) > 128 {
			writeError(w, http.StatusBadRequest, "valid file operation and operation ID required")
			return
		}
		if input.FileOperation.ExpectedFileID != "" {
			id, ok := parseUUIDOrBadRequest(w, input.FileOperation.ExpectedFileID, "expected_file_id")
			if !ok {
				return
			}
			input.FileOperation.ExpectedFileID = uuidToString(id)
		}
	case "", "read":
		input.Action = "read"
	case "branches":
	case "index":
	case "git_history":
		if input.Scope != "checkout" || input.VersionID != "" || input.Offset < 0 || input.Limit < 0 || input.Limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid Git history request")
			return
		}
	case "git_manifest":
		if input.Scope != "checkout" || input.VersionID != "" || input.Offset < 0 || input.Limit < 0 || input.Limit > 500 || (input.GitScope != "branch" && input.GitScope != "commit") || (input.GitScope == "branch" && strings.TrimSpace(input.Target) == "") || (input.GitScope == "commit" && len(input.CommitSHA) != 40 && len(input.CommitSHA) != 64) {
			writeError(w, http.StatusBadRequest, "invalid Git comparison request")
			return
		}
	case "workspace_trash", "repositories", "versions", "manifest", "files", "file", "context", "content", "workspace_files", "file_content", "commits", "lease":
		maxLimit := 500
		if input.Action == "workspace_trash" {
			maxLimit = 100
		}
		if input.Action == "content" || input.Action == "file_content" {
			maxLimit = 64 << 10
		}
		if len(input.VersionID) > 64 || len(input.FilePath) > 4096 || len(input.RelativePath) > 4096 || input.Offset < 0 || input.Limit < 0 || input.Limit > maxLimit || (input.Action != "workspace_trash" && input.Action != "manifest" && input.Action != "repositories" && input.Action != "versions" && input.Action != "workspace_files" && input.Action != "file_content" && input.VersionID == "") || (input.Side != "" && input.Side != "old" && input.Side != "new") || (input.Action == "file_content" && strings.TrimSpace(input.RelativePath) == "" && input.FileID == "") {
			writeError(w, http.StatusBadRequest, "invalid review page request")
			return
		}
	case "edit", "merge_edit":
		if strings.TrimSpace(input.RelativePath) == "" || len(input.RelativePath) > 4096 {
			writeError(w, http.StatusBadRequest, "edited file path required")
			return
		}
		if input.Action == "edit" && len(input.EditBaseSHA) != 64 {
			writeError(w, http.StatusBadRequest, "edit base digest required")
			return
		}

	default:
		writeError(w, http.StatusBadRequest, "unknown review action")
		return
	}
	if input.Action == "file_operation" {
		handled, statusErr := h.tryServeWorkspaceFileOperationStatus(r.Context(), w, ctxWorkspaceID(r.Context()), user, input.CommandID, input.FileOperation)
		if statusErr != nil {
			writeWorkspaceFileCoordinationError(w, statusErr)
			return
		}
		if handled {
			return
		}
	}
	var path, runtimeID, directoryRoot string
	var fileProjectID string
	var fileOwnerKind, fileOwnerID string
	fileOperationsAllowed := false
	if input.ProjectResourceID != "" {
		var authorized bool
		path, runtimeID, authorized = h.authorizeProjectDirectoryRead(w, r, user, input.ProjectResourceID, input.RuntimeID, input.Path)
		if !authorized {
			return
		}
		resource, err := h.Queries.GetProjectResourceInWorkspace(r.Context(), db.GetProjectResourceInWorkspaceParams{ID: parseUUID(input.ProjectResourceID), WorkspaceID: parseUUID(ctxWorkspaceID(r.Context()))})
		if err != nil {
			writeError(w, http.StatusNotFound, "project directory not found")
			return
		}
		var ref localDirectoryRef
		if json.Unmarshal(resource.ResourceRef, &ref) != nil {
			writeError(w, http.StatusBadRequest, "invalid project directory")
			return
		}
		directoryRoot = ref.LocalPath
		if input.Scope != "checkout" && path != directoryRoot {
			writeError(w, http.StatusBadRequest, "path must match the project directory")
			return
		}
		fileProjectID = uuidToString(resource.ProjectID)
		input.ProjectResourceID = uuidToString(resource.ID)
		fileOwnerKind, fileOwnerID = "project_resource", input.ProjectResourceID
	} else {
		id, ok := parseUUIDOrBadRequest(w, input.TaskID, "task_id")
		if !ok {
			return
		}
		task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{ID: id, WorkspaceID: parseUUID(ctxWorkspaceID(r.Context()))})
		if err != nil {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		input.TaskID = uuidToString(task.ID)
		runtime, _, ok := h.requireRuntimeReadAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, uuidToString(task.RuntimeID))
		if !ok {
			return
		}
		if task.IssueID.Valid {
			issue, ok := h.loadIssueForUser(w, r, uuidToString(task.IssueID))
			if !ok {
				return
			}
			fileProjectID = uuidToString(issue.ProjectID)
		} else if uuidToString(runtime.OwnerID) != user {
			writeError(w, http.StatusForbidden, "only the runtime owner can review this run")
			return
		}
		if (input.Action == "workspace_trash" || input.Action == "file_operation" || input.Action == "edit" || input.Action == "merge_edit") && uuidToString(runtime.OwnerID) != user {
			writeError(w, http.StatusForbidden, "only the runtime owner can edit files")
			return
		}
		fileOperationsAllowed = uuidToString(runtime.OwnerID) == user
		path = task.WorkDir.String
		if path == "" {
			writeError(w, http.StatusConflict, "task has not reported a worktree")
			return
		}
		if input.Path != "" {
			base := strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
			selected := strings.ReplaceAll(input.Path, "\\", "/")
			if selected != base && !strings.HasPrefix(selected, base+"/") {
				writeError(w, http.StatusBadRequest, "repository is outside task worktree")
				return
			}
			path = input.Path
		}

		runtimeID = uuidToString(task.RuntimeID)
		binding, ok := h.bindWorkspaceFileTaskOwner(w, r, documentCommentScope{userID: user, workspaceID: parseUUID(ctxWorkspaceID(r.Context()))}, task, DocumentCommentResource{Kind: "workspace_file", TaskID: input.TaskID, RuntimeID: runtimeID, WorkDir: path, ProjectID: fileProjectID})
		if !ok {
			return
		}
		fileOwnerKind, fileOwnerID, fileProjectID = binding.OwnerKind, binding.OwnerID, binding.ProjectID
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	command := protocol.LocalReviewCommand{
		DirectoryRoot: directoryRoot, GitScope: input.GitScope, CommitSHA: input.CommitSHA,
		FileOperation: input.FileOperation,
		FileID:        input.FileID,
		WorkspaceID:   ctxWorkspaceID(r.Context()), RuntimeID: runtimeID,
		ProjectResourceID: input.ProjectResourceID, Scope: input.Scope,
		TaskID: input.TaskID, Path: path, Target: input.Target, Action: input.Action,
		SnapshotID: input.SnapshotID, Comment: input.Comment, ActorID: user,
		CommandID: input.CommandID,
		VersionID: input.VersionID, FilePath: input.FilePath, Offset: input.Offset, Limit: input.Limit,
		RelativePath: input.RelativePath, Recursive: input.Recursive,
		Side:    input.Side,
		IndexID: input.IndexID, Branch: input.Branch, Head: input.Head, Message: input.Message, Paths: input.Paths,
		EditBase: input.EditBase, EditNew: input.EditNew, EditBaseSHA: input.EditBaseSHA,
	}
	var directory workspaceFileDirectory
	if input.Action == "workspace_files" || input.Action == "file_content" || input.Action == "workspace_trash" || input.Action == "file_operation" || identityEdit {
		binding := DocumentCommentResource{Kind: "workspace_file", TaskID: input.TaskID, ProjectResourceID: input.ProjectResourceID, RuntimeID: runtimeID, WorkDir: path, ProjectID: fileProjectID, OwnerKind: fileOwnerKind, OwnerID: fileOwnerID}
		var err error
		directory, err = h.prepareWorkspaceFileDirectory(ctx, command, binding)
		if err != nil {
			writeWorkspaceFileCoordinationError(w, err)
			return
		}
		if input.Action == "file_content" {
			command.RelativePath = directory.binding.RelativePath
		}
	}
	if input.Action == "file_operation" || identityEdit {
		h.forwardWorkspaceFileOperation(w, r.WithContext(ctx), workspaceFileMutation{directory: directory, command: command})
		return
	}
	exchange, err := h.localReviewRelay.enqueue(command)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "local review relay busy")
		return
	}
	result, err := h.localReviewRelay.wait(ctx, exchange)
	if err != nil {
		writeError(w, http.StatusGatewayTimeout, "runtime did not respond; no cloud snapshot is available")
		return
	}
	if result.Error != "" {
		writeError(w, http.StatusConflict, result.Error)
		return
	}
	if input.Action == "workspace_files" && !fileOperationsAllowed {
		if err := restrictWorkspaceFileOperations(&result); err != nil {
			writeError(w, http.StatusBadGateway, "invalid workspace directory response")
			return
		}
	}
	if input.Action == "workspace_files" || input.Action == "file_content" {
		if err := h.addWorkspaceFileIdentities(ctx, directory, input.Action, command.RelativePath, &result); err != nil {
			writeWorkspaceFileCoordinationError(w, err)
			return
		}
	}
	if input.Action == "workspace_trash" {
		if err := h.filterWorkspaceTrash(ctx, workspaceFileMutation{directory: directory, command: command}, &result); err != nil {
			writeWorkspaceFileCoordinationError(w, err)
			return
		}
	}
	result.ClaimToken = ""
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) ClaimLocalReviewRelay(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.localReviewRuntime(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, protocol.LocalReviewClaim{Command: h.localReviewRelay.claim(uuidToString(runtime.WorkspaceID), uuidToString(runtime.ID))})
}

func (h *Handler) ReportLocalReviewRelay(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.localReviewRuntime(w, r)
	if !ok {
		return
	}
	var result protocol.LocalReviewResult
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 12<<20)).Decode(&result) != nil || len(result.Error) > 8000 {
		writeError(w, http.StatusBadRequest, "invalid review result")
		return
	}
	if !h.localReviewRelay.complete(uuidToString(runtime.WorkspaceID), uuidToString(runtime.ID), chi.URLParam(r, "commandId"), result) {
		writeError(w, http.StatusGone, "review request expired or is not owned by runtime")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
