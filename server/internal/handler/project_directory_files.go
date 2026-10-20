package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type ChatProjectDirectoryResponse struct {
	ProjectResourceID string `json:"project_resource_id"`
	DaemonID          string `json:"daemon_id"`
	RuntimeID         string `json:"runtime_id,omitempty"`
	Path              string `json:"path"`
	Label             string `json:"label,omitempty"`
}

func projectDirectoryRuntime(runtimes []db.AgentRuntime, daemonID, userID string) *db.AgentRuntime {
	var selected *db.AgentRuntime
	for i := range runtimes {
		runtime := &runtimes[i]
		if runtime.DaemonID.String != daemonID || runtime.RuntimeMode != "local" || uuidToString(runtime.OwnerID) != userID {
			continue
		}
		if selected == nil || (runtime.Status != "offline" && selected.Status == "offline") || ((runtime.Status == "offline") == (selected.Status == "offline") && runtimeSeenAfter(runtime, selected)) {
			selected = runtime
		}
	}
	return selected
}

func (h *Handler) projectFileDirectories(ctx context.Context, workspaceID, projectID pgtype.UUID, userID string) ([]ChatProjectDirectoryResponse, error) {
	resources, err := h.Queries.ListProjectResourcesInWorkspace(ctx, db.ListProjectResourcesInWorkspaceParams{ProjectID: projectID, WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	runtimes, err := h.Queries.ListAgentRuntimes(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	directories := make([]ChatProjectDirectoryResponse, 0)
	for _, resource := range resources {
		if resource.ResourceType != "local_directory" {
			continue
		}
		var ref localDirectoryRef
		if json.Unmarshal(resource.ResourceRef, &ref) != nil || ref.LocalPath == "" || ref.DaemonID == "" {
			continue
		}
		directory := ChatProjectDirectoryResponse{ProjectResourceID: uuidToString(resource.ID), DaemonID: ref.DaemonID, Path: ref.LocalPath, Label: resource.Label.String}
		if directory.Label == "" {
			directory.Label = ref.Label
		}
		if runtime := projectDirectoryRuntime(runtimes, ref.DaemonID, userID); runtime != nil {
			directory.RuntimeID = uuidToString(runtime.ID)
		}
		directories = append(directories, directory)
	}
	return directories, nil
}

func (h *Handler) authorizeProjectDirectoryRead(w http.ResponseWriter, r *http.Request, userID, resourceID, runtimeID, path string) (string, string, bool) {
	id, ok := parseUUIDOrBadRequest(w, resourceID, "project_resource_id")
	if !ok {
		return "", "", false
	}
	resource, err := h.Queries.GetProjectResourceInWorkspace(r.Context(), db.GetProjectResourceInWorkspaceParams{ID: id, WorkspaceID: parseUUID(ctxWorkspaceID(r.Context()))})
	if err != nil || resource.ResourceType != "local_directory" {
		writeError(w, http.StatusNotFound, "project directory not found")
		return "", "", false
	}
	if _, ok := h.loadProjectForResource(w, r, uuidToString(resource.ProjectID)); !ok {
		return "", "", false
	}
	var ref localDirectoryRef
	if json.Unmarshal(resource.ResourceRef, &ref) != nil || ref.LocalPath == "" || ref.DaemonID == "" || path != ref.LocalPath {
		writeError(w, http.StatusBadRequest, "path must match the project directory")
		return "", "", false
	}
	runtimes, err := h.Queries.ListAgentRuntimes(r.Context(), resource.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot resolve directory runtime")
		return "", "", false
	}
	runtime := projectDirectoryRuntime(runtimes, ref.DaemonID, userID)
	if runtime == nil || (runtimeID != "" && runtimeID != uuidToString(runtime.ID)) {
		writeError(w, http.StatusNotFound, "directory runtime unavailable")
		return "", "", false
	}
	if runtime.Status == "offline" {
		writeError(w, http.StatusConflict, "directory runtime is offline")
		return "", "", false
	}
	return ref.LocalPath, uuidToString(runtime.ID), true
}
