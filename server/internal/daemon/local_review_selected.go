package daemon

import (
	"encoding/json"
	"net/http"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

// The shared operation handler has already authenticated and locked the source.
func (d *Daemon) selectedReviewMutation(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	request := scope.request
	if request.ActorID == "" || request.VersionID != request.SnapshotID {
		http.Error(w, "reviewed selection identity required", http.StatusBadRequest)
		return
	}
	target, err := localreview.TargetWorktree(r.Context(), scope.path, request.Target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if d.localPathLocks != nil {
		paths := []string{scope.path}
		if target != "" {
			paths = append(paths, target)
		}
		release, available := d.localPathLocks.guardReviewPaths(paths)
		if !available {
			http.Error(w, "another task is using the source or target repository", http.StatusConflict)
			return
		}
		defer release()
	}
	store, err := localreview.OpenBlobStore(scope.root, 64<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer store.Close()
	result, err := store.ExecuteSelectedMerge(r.Context(), scope.root, localreview.SelectedMergeOperation{SelectedMergeRequest: localreview.SelectedMergeRequest{Version: localreview.VersionSelection{ID: request.VersionID, Path: scope.path, Target: request.Target}, Paths: request.Paths, Message: request.Message}, CommandID: request.CommandID, ActorID: request.ActorID})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		d.logger.Debug("selected merge response interrupted", "error", err)
	}
}
