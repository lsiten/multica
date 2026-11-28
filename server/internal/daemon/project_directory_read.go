package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Resource commands enter only through the authenticated server relay. They do
// not acquire task provenance or participate in task directory garbage collection.
func (d *Daemon) runProjectDirectoryRead(ctx context.Context, command protocol.LocalReviewCommand) protocol.LocalReviewResult {
	result := protocol.LocalReviewResult{ClaimToken: command.ClaimToken}
	if command.TaskID != "" || command.WorkspaceID == "" || !filepath.IsAbs(command.Path) || !protocol.IsProjectDirectoryRead(command.Action) {
		result.Error = "invalid read-only project directory request"
		return result
	}
	if (command.Scope == "checkout" && !protocol.IsCheckoutRead(command.Action)) || ((command.Action == "git_manifest" || command.Action == "git_history") && command.Scope != "checkout") {
		result.Error = "invalid checkout read request"
		return result
	}
	if err := localReviewOperations.Lock(ctx); err != nil {
		result.Error = err.Error()
		return result
	}
	defer localReviewOperations.Unlock()
	path, err := filepath.EvalSymlinks(command.Path)
	if err != nil {
		result.Error = "project directory unavailable"
		return result
	}
	if command.DirectoryRoot != "" {
		bound, err := filepath.EvalSymlinks(command.DirectoryRoot)
		if err != nil {
			result.Error = "project directory unavailable"
			return result
		}
		rel, err := filepath.Rel(bound, path)
		if err != nil || !filepath.IsLocal(rel) {
			result.Error = "repository is outside project directory"
			return result
		}
	}
	// Git commands can implicitly walk to a parent repository. Require an exact
	// checkout root before exposing index data for a bound resource directory.
	if command.Action == "index" || command.Action == "git_manifest" || command.Action == "branches" {
		if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			result.Error = "project directory is not a Git checkout root"
			return result
		}
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(command.WorkspaceID+"\x00"+command.RuntimeID+"\x00"+command.ProjectResourceID+"\x00"+path)))
	root := filepath.Join(d.cfg.WorkspacesRoot, ".project-directory-reviews", key)
	if command.Scope != "checkout" && (command.Action == "index" || command.Action == "versions" || command.Action == "manifest" || command.Action == "files" || command.Action == "file" || command.Action == "context" || command.Action == "content" || command.Action == "commits") {
		if err := os.MkdirAll(root, 0700); err != nil {
			result.Error = "cannot create project directory cache"
			return result
		}
	}
	request := worktreeReviewRequest{GitScope: command.GitScope, CommitSHA: command.CommitSHA, FilePath: command.FilePath, Side: command.Side, Action: command.Action, Path: command.Path, Target: command.Target, WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, RelativePath: command.RelativePath, Recursive: command.Recursive, VersionID: command.VersionID, Offset: command.Offset, Limit: command.Limit, Scope: command.Scope}
	scope := pagedReviewContext{request: request, path: path, root: root}
	if command.Scope == "checkout" {
		scope, err = d.checkoutReviewScope(ctx, scope)
		if err != nil {
			result.Error = err.Error()
			return result
		}
	}
	if command.Action == "branches" {
		result.Branches, err = localreview.CheckoutReferences(ctx, path)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		return result
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/project-directory/read", nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	capture := &localReviewCapture{header: http.Header{}, status: http.StatusOK}
	d.pagedReviewRead(capture, req, scope)
	if capture.status != http.StatusOK {
		result.Error = capture.body.String()
		return result
	}
	result.Page = append(result.Page, capture.body.Bytes()...)
	return result
}
