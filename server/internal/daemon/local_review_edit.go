package daemon

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

// reviewEdit applies a human or agent file edit to a working file. It is reached
// only after the environment/source/repository locks, so it is mutually
// exclusive with other review mutations. "edit" is the local direct write
// (compare-and-swap, 409 on a changed base); "merge_edit" is the remote
// three-way merge (clean merge applied, conflict keeps the runtime version).
func (d *Daemon) reviewEdit(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	request := scope.request
	repo, err := os.OpenRoot(scope.path)
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusConflict)
		return
	}
	defer repo.Close()
	var result any
	switch request.Action {
	case "edit":
		res, e := localreview.ApplyWorkingFileEdit(r.Context(), repo, localreview.EditWorkingFileRequest{
			FilePath:   request.RelativePath,
			BaseSHA256: request.EditBaseSHA,
			NewContent: []byte(request.EditNew),
		})
		if e != nil {
			http.Error(w, e.Error(), http.StatusConflict)
			return
		}
		result = res
	case "merge_edit":
		res, e := localreview.MergeWorkingFile(r.Context(), repo, localreview.MergeWorkingFileRequest{
			FilePath: request.RelativePath,
			Base:     []byte(request.EditBase),
			New:      []byte(request.EditNew),
		})
		if e != nil {
			http.Error(w, e.Error(), http.StatusConflict)
			return
		}
		result = res
	default:
		http.Error(w, "unsupported edit action", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		d.logger.Debug("edit response interrupted", "error", err)
	}
}
