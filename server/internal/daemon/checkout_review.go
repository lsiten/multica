package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) checkoutReviewScope(ctx context.Context, scope pagedReviewContext) (pagedReviewContext, error) {
	request := scope.request
	if request.RuntimeID == "" || !protocol.IsCheckoutRead(request.Action) {
		return scope, errors.New("checkout reads require a runtime and read-only action")
	}
	path, err := filepath.EvalSymlinks(request.Path)
	if err != nil || path != scope.path {
		return scope, errors.New("checkout directory is unavailable")
	}
	if request.Action != "repositories" {
		root, err := worktreeGit(ctx, path, "rev-parse", "--show-toplevel")
		if err != nil || root != path {
			return scope, errors.New("select the repository root")
		}
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(request.WorkspaceID+"\x00"+request.RuntimeID+"\x00"+path)))
	scope.root = filepath.Join(d.cfg.WorkspacesRoot, ".checkout-reviews", key)
	if err := os.MkdirAll(scope.root, 0700); err != nil {
		return scope, err
	}
	return scope, nil
}

func (d *Daemon) checkoutHistoryRead(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	limit := scope.request.Limit
	if limit == 0 {
		limit = 100
	}
	page, err := localreview.CheckoutHistory(r.Context(), scope.path, localreview.FilePageRequest{Offset: scope.request.Offset, Limit: limit})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(page); err != nil {
		d.logger.Debug("checkout history response interrupted", "error", err)
	}
}
