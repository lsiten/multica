package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/workspacefiles"
)

var errWorkspaceFileEffectsUnknown = errors.New("workspace file operation effects are unconfirmed")
var errWorkspaceFileRejected = errors.New("workspace file operation was rejected without changes")

const workspaceFileEffectsUnknownHeader = "X-Multica-File-Effects-Unknown"
const workspaceFileRejectedHeader = "X-Multica-File-Rejected"

func (d *Daemon) reviewFileOperation(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	if scope.request.FileOperation == nil {
		http.Error(w, "file operation required", http.StatusBadRequest)
		return
	}
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
	result, err := workspacefiles.Apply(r.Context(), files, workspacefiles.Request{CommandID: scope.request.CommandID, OperationID: scope.request.FileOperationID, ActorID: scope.request.ActorID, Operation: *scope.request.FileOperation})
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
		d.logger.Debug("workspace file operation response interrupted", "error", err)
	}
}

func (d *Daemon) workspaceTrashRead(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	files, closeFiles, err := d.openWorkspaceFileScope(scope)
	if err != nil {
		http.Error(w, "workspace directory unavailable", http.StatusConflict)
		return
	}
	defer closeFiles()
	limit := scope.request.Limit
	if limit == 0 {
		limit = 100
	}
	page, err := workspacefiles.ListTrash(r.Context(), files, workspacefiles.TrashPageRequest{Offset: scope.request.Offset, Limit: limit})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	page.Path = scope.request.Path
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(page); err != nil {
		d.logger.Debug("workspace trash page interrupted", "error", err)
	}
}

func (d *Daemon) openWorkspaceFileScope(scope pagedReviewContext) (workspacefiles.Scope, func(), error) {
	working, err := os.OpenRoot(scope.path)
	if err != nil {
		return workspacefiles.Scope{}, nil, err
	}
	cache, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		working.Close()
		return workspacefiles.Scope{}, nil, err
	}
	defer cache.Close()
	namespace := workspaceFileStorageNamespace(scope.request.WorkspaceID, scope.path)
	if err := cache.MkdirAll(namespace, 0o700); err != nil {
		working.Close()
		return workspacefiles.Scope{}, nil, err
	}
	storage, err := cache.OpenRoot(namespace)
	if err != nil {
		working.Close()
		return workspacefiles.Scope{}, nil, err
	}
	return workspacefiles.Scope{Working: working, Storage: storage}, func() { working.Close(); storage.Close() }, nil
}

func workspaceFileStorageNamespace(workspace, path string) string {
	hash := sha256.Sum256([]byte(workspace + "\x00" + path))
	return filepath.Join(".workspace-file-operations", hex.EncodeToString(hash[:]))
}
