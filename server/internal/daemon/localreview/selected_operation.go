package localreview

import (
	"context"
	"errors"
	"slices"
	"strings"
)

type SelectedMergeOperation struct {
	SelectedMergeRequest
	CommandID, ActorID string
}

type SelectedMergeResult struct {
	Kind      string   `json:"kind"`
	VersionID string   `json:"version_id"`
	Target    string   `json:"target"`
	Paths     []string `json:"paths"`
	Commit    string   `json:"commit"`
	Conflicts []string `json:"conflicts"`
}

// ExecuteSelectedMerge is called under the owning runtime's task/source/target
// locks. Prepared receipts recover a lost response, never repeat publication.
func (s *BlobStore) ExecuteSelectedMerge(ctx context.Context, root string, request SelectedMergeOperation) (SelectedMergeResult, error) {
	result := SelectedMergeResult{Kind: "selected_merge", VersionID: request.Version.ID, Target: request.Version.Target, Paths: slices.Clone(request.Paths), Conflicts: []string{}}
	if strings.TrimSpace(request.CommandID) == "" || len(request.CommandID) > 128 || request.ActorID == "" || len(request.Paths) == 0 || len(request.Paths) > maxVersionFiles {
		return result, ErrInvalidReviewVersion
	}
	slices.Sort(result.Paths)
	for index, path := range result.Paths {
		if !validVersionPath(path) || (index > 0 && result.Paths[index-1] == path) {
			return result, ErrInvalidReviewVersion
		}
	}
	key := RecordKey(Snapshot{Path: request.Version.Path, Target: "selected-merge:" + request.CommandID})
	record, err := LoadRecord(root, key)
	if err != nil {
		return result, err
	}
	if record.PreparedRequest != nil {
		event := record.PreparedRequest
		if event.Kind != "merge_selected" || event.CommandID != request.CommandID || event.ActorID != request.ActorID || event.VersionID != request.Version.ID || event.Comment != request.Message || !slices.Equal(event.Paths, result.Paths) || record.Snapshot == nil || record.Snapshot.Path != request.Version.Path || record.Snapshot.Target != request.Version.Target {
			return result, ErrIndexOperationMismatch
		}
		if record.State != "selected_merge_completed" {
			if !validGitObjectID(record.PreparedCommit) || !ContainsMerge(ctx, *record.Snapshot, record.PreparedCommit) {
				return result, ErrIndexRecoveryRequired
			}
			record.State, record.MergedCommit = "selected_merge_completed", record.PreparedCommit
			if err := SaveRecord(root, key, record); err != nil {
				return result, err
			}
		}
		if !validGitObjectID(record.MergedCommit) {
			return result, ErrInvalidReviewVersion
		}
		result.Commit = record.MergedCommit
		return result, nil
	}
	version, err := s.LoadVersion(ctx, request.Version.ID)
	if err != nil {
		return result, err
	}
	snapshot := version.RecoverySnapshot(request.Version.ID)
	commit, err := s.MergeSelectedPrepared(ctx, request.SelectedMergeRequest, func(commit string) error {
		record = Record{VersionID: request.Version.ID, SnapshotID: request.Version.ID, State: "selected_merge_prepared", Snapshot: &snapshot, PreparedCommit: commit, CommandID: request.CommandID, PreparedRequest: &Event{Kind: "merge_selected", VersionID: request.Version.ID, SnapshotID: request.Version.ID, CommandID: request.CommandID, ActorID: request.ActorID, Comment: request.Message, Paths: result.Paths}}
		return SaveRecord(root, key, record)
	})
	if err != nil {
		var conflict *SelectedMergeConflictError
		if errors.As(err, &conflict) && len(conflict.Files) > 0 {
			result.Conflicts = conflict.Files
			return result, nil
		}
		return result, err
	}
	record.State, record.MergedCommit = "selected_merge_completed", commit
	if err := SaveRecord(root, key, record); err != nil {
		return result, err
	}
	result.Commit = commit
	return result, nil
}
