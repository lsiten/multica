package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/application"
)

func (h *Handler) ListApplicationRevisions(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, id, _, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	var before int64
	if value := r.URL.Query().Get("before_revision"); value != "" {
		var err error
		before, err = strconv.ParseInt(value, 10, 64)
		if err != nil || before < 1 {
			writeError(w, http.StatusBadRequest, "before_revision must be a positive integer")
			return
		}
	}
	page, err := h.applicationService().RevisionHistory(r.Context(), application.RevisionRef{WorkspaceID: workspaceID, ApplicationID: id, Revision: before})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) GetApplicationRevision(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, id, _, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(chi.URLParam(r, "revision"), 10, 64)
	if err != nil || revision < 1 {
		writeError(w, http.StatusBadRequest, "revision must be a positive integer")
		return
	}
	view, err := h.applicationService().ReadRevision(r.Context(), application.RevisionRef{WorkspaceID: workspaceID, ApplicationID: id, Revision: revision})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) RestoreApplicationRevision(w http.ResponseWriter, r *http.Request) {
	current, workspaceID, id, member, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	actor, ok := h.applicationActor(w, r, workspaceID, member, current.ProjectID)
	if !ok {
		return
	}
	var input struct {
		Revision       int64 `json:"revision"`
		TargetRevision int64 `json:"target_revision"`
	}
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	if input.Revision < 1 || input.TargetRevision < 1 {
		writeError(w, http.StatusBadRequest, "revision and target_revision must be positive integers")
		return
	}
	view, err := h.applicationService().RestoreRevision(r.Context(), application.RestoreRevisionRequest{Target: application.RevisionRef{WorkspaceID: workspaceID, ApplicationID: id, Revision: input.TargetRevision}, ExpectedRevision: input.Revision, Actor: actor})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	h.applicationChanged(r, workspaceID, actor, view.ID)
	writeJSON(w, http.StatusOK, view)
}
