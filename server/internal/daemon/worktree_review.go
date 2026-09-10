package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type worktreeReviewRequest struct {
	IndexID       string   `json:"index_id,omitempty"`
	Branch        string   `json:"branch,omitempty"`
	Head          string   `json:"head,omitempty"`
	Message       string   `json:"message,omitempty"`
	Paths         []string `json:"paths,omitempty"`
	legacyRuntime *execenv.ReviewRuntime
	TaskID        string `json:"task_id"`
	WorkspaceID   string `json:"workspace_id"`
	RuntimeID     string `json:"runtime_id"`
	Path          string `json:"path"`
	Target        string `json:"target"`
	Action        string `json:"action"`
	SnapshotID    string `json:"snapshot_id"`
	VersionID     string `json:"version_id,omitempty"`
	FilePath      string `json:"file_path,omitempty"`
	Side          string `json:"side,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Comment       string `json:"comment"`
	CommandID     string `json:"command_id"`
	ActorID       string `json:"actor_id"`
}

type worktreeReviewResponse struct {
	localreview.Snapshot
	Review localreview.RecordView `json:"review"`
}

// resolveReviewRoot binds a real repository to its daemon-owned task environment.
func (d *Daemon) resolveReviewRoot(ctx context.Context, request worktreeReviewRequest) (string, string, error) {
	path, err := filepath.EvalSymlinks(request.Path)
	if err != nil {
		return d.resolveBoundReviewDirectory(ctx, request, request.Path)
	}
	root, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return d.resolveBoundReviewDirectory(ctx, request, path)
	}
	for parent := path; parent != root; parent = filepath.Dir(parent) {
		parentRel, e := filepath.Rel(root, parent)
		if e != nil {
			return "", "", e
		}
		logicalParent := filepath.Join(d.cfg.WorkspacesRoot, parentRel)
		owner, err := d.gcTaskDirOwner(logicalParent)
		if err == nil && owner.TaskID == request.TaskID && owner.WorkspaceID == request.WorkspaceID {
			if gitRoot, gitErr := worktreeGit(ctx, path, "rev-parse", "--show-toplevel"); gitErr == nil {
				relative, relErr := filepath.Rel(parent, gitRoot)
				if relErr == nil && filepath.IsLocal(relative) {
					path = gitRoot
				}
			}
			return path, logicalParent, nil
		}
	}
	return d.resolveBoundReviewDirectory(ctx, request, path)
}

func (d *Daemon) resolveBoundReviewDirectory(ctx context.Context, request worktreeReviewRequest, path string) (string, string, error) {
	report, err := ScanDiskUsage(d.cfg.WorkspacesRoot, d.cfg.GCArtifactPatterns)
	if err != nil {
		return "", "", err
	}
	for _, entry := range report.Tasks {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		owner, err := d.gcTaskDirOwner(entry.Path)
		if err != nil || owner.TaskID != request.TaskID || owner.WorkspaceID != request.WorkspaceID {
			continue
		}
		binding, err := execenv.ReadReviewDirectory(entry.Path)
		if err != nil || binding.TaskID != owner.TaskID || binding.WorkspaceID != owner.WorkspaceID || !filepath.IsAbs(binding.Path) {
			continue
		}
		canonical, err := filepath.EvalSymlinks(binding.Path)
		if err != nil || canonical != binding.Path {
			continue
		}
		if binding.SourcePath == request.Path && binding.Commit != "" && binding.Branch != "" {
			return canonical, entry.Path, nil
		}
		rel, err := filepath.Rel(canonical, path)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		return path, entry.Path, nil
	}
	archive := execenv.ReviewArchivePath(d.cfg.WorkspacesRoot, request.WorkspaceID, request.TaskID)
	binding, err := execenv.ReadReviewDirectory(archive)
	if err == nil && binding.WorkspaceID == request.WorkspaceID && binding.TaskID == request.TaskID {
		canonical, e := filepath.EvalSymlinks(binding.Path)
		if e == nil && canonical == binding.Path {
			if binding.SourcePath == request.Path && binding.Commit != "" && binding.Branch != "" {
				return canonical, archive, nil
			}
			if rel, e := filepath.Rel(canonical, path); e == nil && filepath.IsLocal(rel) {
				return path, archive, nil
			}
		}
	}
	return "", "", errors.New("directory has no matching runtime task binding; run the task with an updated daemon first")
}

func (d *Daemon) worktreeReviewHandler() http.HandlerFunc {
	return d.reviewOperationHandler(false)
}

func (d *Daemon) reviewOperationHandler(forwarded bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.client == nil || d.client.Token() == "" || r.Header.Get("Origin") != "" || r.Header.Get("X-Multica-Profile") != d.cfg.Profile ||
			subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+d.client.Token())) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request worktreeReviewRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&request); err != nil || request.TaskID == "" || request.WorkspaceID == "" || !filepath.IsAbs(request.Path) || len(request.Comment) > 8000 || len(request.CommandID) > 128 || len(request.Paths) > 10000 || len(request.Message) > 8000 {
			http.Error(w, "task, workspace and absolute repository path are required", http.StatusBadRequest)
			return
		}
		if !forwarded && !isReadReviewAction(request.Action) && request.RuntimeID == "" {
			http.Error(w, "runtime identity required for local decisions", http.StatusForbidden)
			return
		}
		if !isReadReviewAction(request.Action) && strings.TrimSpace(request.CommandID) == "" {
			http.Error(w, "operation ID required for review decisions", http.StatusBadRequest)
			return
		}
		if request.RuntimeID != "" {
			d.mu.Lock()
			owned := false
			if workspace := d.workspaces[request.WorkspaceID]; workspace != nil {
				for _, id := range workspace.runtimeIDs {
					if id == request.RuntimeID {
						owned = true
						break
					}
				}
			}
			d.mu.Unlock()
			if !owned {
				http.Error(w, "runtime is not registered in this local workspace", http.StatusForbidden)
				return
			}
		}
		if !forwarded {
			directoryTask, err := d.reviewDirectoryTask(r.Context(), request)
			if err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			request.TaskID = directoryTask
		}
		if !forwarded && !isReadReviewAction(request.Action) {
			_, root, err := d.resolveReviewRoot(r.Context(), request)
			if err != nil {
				http.Error(w, "local task unavailable", http.StatusForbidden)
				return
			}
			binding, err := execenv.ReadReviewRuntime(root)
			if errors.Is(err, os.ErrNotExist) {
				binding, err = d.legacyReviewRuntime(r.Context(), request)
				if err == nil {
					request.legacyRuntime = &binding
				}
			}
			if err != nil || binding.WorkspaceID != request.WorkspaceID || binding.TaskID != request.TaskID || binding.RuntimeID != request.RuntimeID {
				http.Error(w, "local runtime task binding unavailable", http.StatusForbidden)
				return
			}
			request.ActorID = "local-runtime:" + request.RuntimeID
		}
		if !forwarded && request.Action == "merge" {
			if result, recovered := d.recoverRemoteMerge(r.Context(), request, ""); recovered {
				if result.Error != "" {
					http.Error(w, result.Error, http.StatusConflict)
					return
				}
				if len(result.Page) > 0 {
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(result.Page); err != nil {
						d.logger.Debug("local review recovery response interrupted", "error", err)
					}
					return
				}
				var response worktreeReviewResponse
				if json.Unmarshal(result.Snapshot, &response.Snapshot) != nil || json.Unmarshal(result.Review, &response.Review) != nil {
					http.Error(w, "cannot decode recovered review", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					d.logger.Debug("local review recovery response interrupted", "error", err)
				}
				return
			}
		}
		if err := localReviewOperations.Lock(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusRequestTimeout)
			return
		}
		defer localReviewOperations.Unlock()
		path, root, err := d.resolveReviewRoot(r.Context(), request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		actorName := ""
		if isPagedReviewRead(request.Action) {
			d.pagedReviewRead(w, r, pagedReviewContext{request: request, path: path, root: root})
			return
		}
		if request.Action == "branches" {
			branches, branchErr := localreview.Branches(r.Context(), path)
			if branchErr != nil {
				http.Error(w, branchErr.Error(), http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(struct {
				Branches []string `json:"branches"`
			}{branches}); err != nil {
				d.logger.Debug("local branch response interrupted", "error", err)
			}
			return
		}
		if !forwarded && request.Action != "" && request.Action != "read" {
			binding, err := execenv.ReadReviewRuntime(root)
			if errors.Is(err, os.ErrNotExist) && request.legacyRuntime != nil {
				binding, err = *request.legacyRuntime, nil
			}
			if err != nil || binding.WorkspaceID != request.WorkspaceID || binding.TaskID != request.TaskID || binding.RuntimeID != request.RuntimeID {
				http.Error(w, "local runtime task binding unavailable", http.StatusForbidden)
				return
			}
			request.ActorID = "local-runtime:" + request.RuntimeID
			actorName = "Local runtime owner"
		}
		if request.Action != "" && request.Action != "read" {
			release, ok := d.reserveEnvRootForGC(root)
			if !ok {
				http.Error(w, "task is running; wait until it finishes", http.StatusConflict)
				return
			}
			defer release()
			fsRoot, e := os.OpenRoot(d.cfg.WorkspacesRoot)
			if e != nil {
				http.Error(w, "workspace unavailable", http.StatusConflict)
				return
			}
			defer fsRoot.Close()
			rel, e := filepath.Rel(d.cfg.WorkspacesRoot, root)
			if e != nil {
				http.Error(w, "invalid workspace", http.StatusConflict)
				return
			}
			claim, _, e := execenv.LockEnvRootForReuse(fsRoot, rel, root)
			if e != nil || claim == nil {
				http.Error(w, "workspace is busy", http.StatusConflict)
				return
			}
			defer claim.Release()
			if request.legacyRuntime != nil {
				if e := persistLegacyReviewRuntime(root, *request.legacyRuntime); e != nil {
					http.Error(w, e.Error(), http.StatusConflict)
					return
				}
			}
			releaseSource, e := d.claimReviewSource(r.Context(), path, root)
			if e != nil {
				http.Error(w, e.Error(), http.StatusConflict)
				return
			}
			defer releaseSource()
			unlock, e := execenv.LockRepositoryForReview(path)
			if e != nil {
				http.Error(w, "repository is busy", http.StatusConflict)
				return
			}
			defer unlock()
		}
		if protocol.IsLocalIndexMutation(request.Action) {
			d.localIndexMutation(w, r, pagedReviewContext{request: request, path: path, root: root})
			return
		}
		if request.Action == "merge_selected" {
			d.selectedReviewMutation(w, r, pagedReviewContext{request: request, path: path, root: root})
			return
		}
		if request.VersionID != "" && !isReadReviewAction(request.Action) {
			d.pagedReviewDecision(w, r, pagedReviewDecisionContext{pagedReviewContext: pagedReviewContext{request: request, path: path, root: root}, actorName: actorName})
			return
		}
		var snapshot localreview.Snapshot
		if binding, bindingErr := execenv.ReadReviewDirectory(root); bindingErr == nil && binding.SourcePath == request.Path && binding.Commit != "" {
			snapshot, err = localreview.ReadCommitted(r.Context(), localreview.CommittedRequest{Path: path, Branch: binding.Branch, Head: binding.Commit, Target: request.Target})
		} else {
			snapshot, err = localreview.Read(r.Context(), path, request.Target)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		key := localreview.RecordKey(localreview.Snapshot{Path: path, Target: snapshot.Target})
		record, err := localreview.LoadRecord(root, key)
		if err != nil {
			http.Error(w, "cannot read review record", http.StatusInternalServerError)
			return
		}
		if request.CommandID != "" && request.Action != "" && request.Action != "read" {
			for _, event := range record.Events {
				if event.CommandID != request.CommandID {
					continue
				}
				if event.Kind != request.Action || event.SnapshotID != request.SnapshotID || event.Comment != request.Comment || event.ActorID != request.ActorID {
					http.Error(w, "operation ID already used with different review data", http.StatusConflict)
					return
				}
				request.Action = "read"
				break
			}
		}
		if request.Action != "" && request.Action != "read" {
			if record.State == "merged" && record.SourceHead == snapshot.Head && !snapshot.Dirty {
				http.Error(w, "this source revision is already merged", http.StatusConflict)
				return
			}
			if snapshot.ID != request.SnapshotID {
				http.Error(w, "changes have moved; reload and review again", http.StatusConflict)
				return
			}
			if snapshot.Head == "" {
				http.Error(w, "select a repository before reviewing", http.StatusConflict)
				return
			}
			if record.SnapshotID != snapshot.ID {
				record = localreview.Record{State: "draft", Events: record.Events}
			}
			record.SnapshotID, record.SourceHead = snapshot.ID, snapshot.Head
			switch request.Action {
			case "submit":
				record.State = "open"
			case "approve":
				record.State = "approved"
			case "request_changes":
				record.State = "changes_requested"
			case "merge":
				if record.State != "approved" {
					http.Error(w, "approve this snapshot before merging", http.StatusConflict)
					return
				}
				if d.localPathLocks != nil {
					target, e := localreview.TargetWorktree(r.Context(), snapshot.Path, snapshot.Target)
					if e != nil {
						http.Error(w, "cannot inspect target checkout", http.StatusConflict)
						return
					}
					paths := []string{snapshot.Path}
					if target != "" {
						paths = append(paths, target)
					}
					unlockPaths, available := d.localPathLocks.guardReviewPaths(paths)
					if !available {
						http.Error(w, "another task is using the source or target repository", http.StatusConflict)
						return
					}
					defer unlockPaths()
				}
				commit, e := localreview.MergePrepared(r.Context(), snapshot, func(commit string) error {
					record.PreparedCommit, record.Snapshot = commit, &snapshot
					record.CommandID = request.CommandID
					record.PreparedRequest = &localreview.Event{
						Kind: "merge", SnapshotID: snapshot.ID, CommandID: request.CommandID,
						ActorID: request.ActorID, ActorName: actorName, Comment: request.Comment,
						CreatedAt: time.Now().UTC(),
					}
					return localreview.SaveRecord(root, key, record)
				})
				if e != nil {
					http.Error(w, e.Error(), http.StatusConflict)
					return
				}
				record.State, record.MergedCommit = "merged", commit
			default:
				http.Error(w, "unknown review action", http.StatusBadRequest)
				return
			}
			record.Comment = request.Comment
			record.Events = append(record.Events, localreview.Event{
				Kind: request.Action, SnapshotID: snapshot.ID, Comment: request.Comment, ActorID: request.ActorID, ActorName: actorName, CommandID: request.CommandID, CreatedAt: time.Now().UTC(),
			})
			if err := localreview.SaveRecord(root, key, record); err != nil {
				http.Error(w, "review operation finished but record could not be saved; reload before retrying", http.StatusInternalServerError)
				return
			}
		}
		if record.SnapshotID != snapshot.ID && !(record.State == "merged" && record.SourceHead == snapshot.Head && !snapshot.Dirty) {
			record = localreview.Record{SnapshotID: snapshot.ID, State: "draft", Events: record.Events}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(worktreeReviewResponse{Snapshot: snapshot, Review: record.View()}); err != nil {
			d.logger.Debug("local review response interrupted", "error", err)
		}
	}
}
