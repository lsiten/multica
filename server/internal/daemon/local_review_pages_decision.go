package daemon

import (
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"net/http"
	"time"
)

type pagedReviewDecisionContext struct {
	pagedReviewContext
	actorName string
}

// pagedReviewDecision runs inside the existing environment/source/repository locks.
func (d *Daemon) pagedReviewDecision(w http.ResponseWriter, r *http.Request, scope pagedReviewDecisionContext) {
	request := scope.request
	if request.VersionID == "" || request.VersionID != request.SnapshotID {
		http.Error(w, "reviewed version identity required", http.StatusBadRequest)
		return
	}
	store, err := localreview.OpenBlobStore(scope.root, 64<<20)
	if err != nil {
		http.Error(w, "cannot open local review cache", http.StatusConflict)
		return
	}
	defer store.Close()
	version, err := store.LoadVersion(r.Context(), request.VersionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if version.Header.Repository != scope.path || version.Header.Target != request.Target {
		http.Error(w, "review version belongs to another repository or target", http.StatusForbidden)
		return
	}
	if err := store.TouchVersion(r.Context(), request.VersionID, time.Now()); err != nil {
		http.Error(w, "review version expired; refresh the review", http.StatusConflict)
		return
	}
	key := localreview.RecordKey(localreview.Snapshot{Path: scope.path, Target: request.Target})
	record, err := localreview.LoadRecord(scope.root, key)
	if err != nil {
		http.Error(w, "cannot read review record", http.StatusConflict)
		return
	}
	replayed := false
	for _, event := range record.Events {
		if event.CommandID != request.CommandID {
			continue
		}
		if event.Kind != request.Action || event.SnapshotID != request.VersionID || event.ActorID != request.ActorID || event.Comment != request.Comment {
			http.Error(w, "operation ID already used with different review data", http.StatusConflict)
			return
		}
		replayed = true
		break
	}
	if !replayed {
		if record.State == "merged" && record.SourceHead == version.Header.Head && !version.Header.Dirty {
			http.Error(w, "this source revision is already merged", http.StatusConflict)
			return
		}
		selection := localreview.VersionSelection{ID: request.VersionID, Path: scope.path, Target: request.Target}
		if _, err := store.VerifyVersion(r.Context(), selection); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if record.SnapshotID != request.VersionID {
			record = localreview.Record{State: "draft", Events: record.Events}
		}
		record.SnapshotID, record.VersionID, record.SourceHead = request.VersionID, request.VersionID, version.Header.Head
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
			snapshot := version.RecoverySnapshot(request.VersionID)
			if d.localPathLocks != nil {
				target, err := localreview.TargetWorktree(r.Context(), scope.path, request.Target)
				if err != nil {
					http.Error(w, "cannot inspect target checkout", http.StatusConflict)
					return
				}
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
			commit, err := store.MergeVersionPrepared(r.Context(), selection, func(commit string) error {
				record.PreparedCommit, record.Snapshot, record.CommandID = commit, &snapshot, request.CommandID
				record.PreparedRequest = &localreview.Event{VersionID: request.VersionID, Kind: "merge", SnapshotID: request.VersionID, CommandID: request.CommandID, ActorID: request.ActorID, ActorName: scope.actorName, Comment: request.Comment, CreatedAt: time.Now().UTC()}
				return localreview.SaveRecord(scope.root, key, record)
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			record.State, record.MergedCommit = "merged", commit
		default:
			http.Error(w, "unknown review decision", http.StatusBadRequest)
			return
		}
		record.Comment = request.Comment
		record.Events = append(record.Events, localreview.Event{VersionID: request.VersionID, Kind: request.Action, SnapshotID: request.VersionID, CommandID: request.CommandID, ActorID: request.ActorID, ActorName: scope.actorName, Comment: request.Comment, CreatedAt: time.Now().UTC()})
		if err := localreview.SaveRecord(scope.root, key, record); err != nil {
			http.Error(w, "operation finished but receipt could not be saved; refresh before retrying", http.StatusInternalServerError)
			return
		}
	}
	if record.SnapshotID != request.VersionID {
		record = localreview.Record{SnapshotID: request.VersionID, State: "draft", Events: record.Events}
	}
	page, err := version.FilePage(localreview.FilePageRequest{Limit: 100})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	response := pagedReviewManifest{VersionID: request.VersionID, Header: version.Header, Page: page, Review: record.View()}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		d.logger.Debug("local review decision response interrupted", "error", err)
	}
}
