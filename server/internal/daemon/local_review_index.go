package daemon

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

type localIndexView struct {
	UnstagedVersionID string                  `json:"unstaged_version_id,omitempty"`
	StagedVersionID   string                  `json:"staged_version_id,omitempty"`
	Kind              string                  `json:"kind"`
	VersionID         string                  `json:"version_id"`
	Status            localreview.IndexStatus `json:"status"`
}

func (d *Daemon) localIndexRead(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	status, err := localreview.ReadIndexStatus(r.Context(), scope.path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	store, err := localreview.OpenBlobStore(scope.root, 64<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer store.Close()
	release, err := store.BeginRead(r.Context(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer release()
	version, err := store.CaptureWorking(r.Context(), localreview.WorkingVersionRequest{Path: scope.path, Target: status.Branch})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	after, err := localreview.ReadIndexStatus(r.Context(), scope.path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if status.IndexID != after.IndexID || status.Head != after.Head || status.Branch != after.Branch {
		http.Error(w, localreview.ErrIndexStateChanged.Error(), http.StatusConflict)
		return
	}
	id, err := store.SaveVersion(r.Context(), version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	stagedID := ""
	hasStaged, hasUnstaged, conflicted := false, false, false
	for _, file := range status.Files {
		hasStaged = hasStaged || file.Staged
		hasUnstaged = hasUnstaged || file.Unstaged
		conflicted = conflicted || file.Conflicted
	}
	unstagedID := ""
	if hasUnstaged && !conflicted {
		unstaged, err := store.CaptureUnstaged(r.Context(), scope.path, status.IndexID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		unstagedID, err = store.SaveVersion(r.Context(), unstaged)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	}
	if hasStaged && !conflicted {
		staged, err := store.CaptureStaged(r.Context(), scope.path, status.IndexID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		stagedID, err = store.SaveVersion(r.Context(), staged)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(localIndexView{Kind: "index", VersionID: id, Status: status, StagedVersionID: stagedID, UnstagedVersionID: unstagedID}); err != nil {
		d.logger.Debug("index status response interrupted", "error", err)
	}
}

// Called only after the shared mutation binding/GC/source/repository locks.
func (d *Daemon) localIndexMutation(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	request := scope.request
	if request.ActorID == "" || len(request.Message) > 8000 || len(request.Paths) > 1000 {
		http.Error(w, "invalid index operation", http.StatusBadRequest)
		return
	}
	store, err := localreview.OpenBlobStore(scope.root, 64<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer store.Close()
	result, err := store.ExecuteIndexOperation(r.Context(), scope.root, localreview.IndexOperationRequest{Path: scope.path, CommandID: request.CommandID, ActorID: request.ActorID, Action: request.Action, VersionID: request.VersionID, IndexID: request.IndexID, Head: request.Head, Branch: request.Branch, Message: request.Message, Paths: request.Paths})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(struct {
		Kind   string                           `json:"kind"`
		Result localreview.IndexOperationResult `json:"result"`
	}{"index_result", result}); err != nil {
		d.logger.Debug("index operation response interrupted", "error", err)
	}
}
