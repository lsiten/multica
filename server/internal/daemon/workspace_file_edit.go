package daemon

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/multica-ai/multica/server/internal/daemon/workspacefiles"
)

func (d *Daemon) reviewAdmittedFileEdit(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	if d.localPathLocks == nil {
		http.Error(w, "file operation locks unavailable", http.StatusConflict)
		return
	}
	release, available := d.localPathLocks.guardReviewPaths([]string{scope.path})
	if !available {
		http.Error(w, "another task is using this directory", http.StatusConflict)
		return
	}
	defer release()
	files, closeFiles, err := d.openWorkspaceFileScope(scope)
	if err != nil {
		http.Error(w, "workspace directory unavailable", http.StatusConflict)
		return
	}
	defer closeFiles()
	request := scope.request
	result, err := workspacefiles.ApplyEdit(r.Context(), files, workspacefiles.EditRequest{CommandID: request.CommandID, OperationID: request.FileOperationID, FileID: request.FileID, Action: request.Action, Path: request.RelativePath, BaseSHA: request.EditBaseSHA, Base: request.EditBase, New: request.EditNew})
	if err != nil {
		if errors.Is(err, workspacefiles.ErrOperationUnconfirmed) {
			w.Header().Set(workspaceFileEffectsUnknownHeader, "true")
		} else if errors.Is(err, workspacefiles.ErrOperationRejected) {
			w.Header().Set(workspaceFileRejectedHeader, "true")
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		d.logger.Debug("workspace edit response interrupted", "error", err)
	}
}
