package localreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// InspectDecision reads receipts and Git facts. It never creates a review,
// approves a revision, refreshes a lease, or updates a Git ref.
func InspectDecision(ctx context.Context, root, repository string) (protocol.WorktreeRepositoryLifecycle, error) {
	result := protocol.WorktreeRepositoryLifecycle{Path: repository, NextAction: protocol.WorktreeReview, Reason: "no_target"}
	path, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return result, err
	}
	branches, err := Branches(ctx, path)
	if err != nil {
		return result, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return result, err
	}
	var selected Record
	var expected Snapshot
	found := false
	for _, entry := range entries {
		key := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), ".local-review-"), ".json")
		if !strings.HasPrefix(entry.Name(), ".local-review-") || !strings.HasSuffix(entry.Name(), ".json") || !validBlobID(key) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		record, err := LoadRecord(root, key)
		if err != nil {
			return result, err
		}
		switch record.State {
		case "draft", "open", "approved", "changes_requested", "merged":
		default:
			continue
		}
		snapshot, err := inventorySnapshot(ctx, inventoryReceipt{root: root, path: path, key: key, branches: branches, record: record})
		if err != nil {
			return result, err
		}
		if snapshot.Path != path || snapshot.Target == "" {
			continue
		}
		if found {
			result.NextAction, result.Reason = protocol.WorktreeUnknown, "multiple_reviews"
			return result, nil
		}
		selected, expected, found = record, snapshot, true
	}
	if !found {
		return result, nil
	}
	result.Target, result.ReviewState = expected.Target, selected.State
	head, err := trimmed(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	status, err := git(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return result, err
	}
	if status != "" {
		result.Reason = "dirty"
		return result, nil
	}
	if selected.State == "merged" && selected.SourceHead == head && ContainsMerge(ctx, expected, selected.MergedCommit) {
		result.NextAction, result.Reason = protocol.WorktreeCleanup, "merged"
		return result, nil
	}
	if expected.ID != selected.SnapshotID || expected.Head != head || expected.Dirty || !TargetUnchanged(ctx, expected) {
		result.Reason = "stale_review"
		return result, nil
	}
	branch, err := trimmed(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != expected.Branch {
		result.Reason = "stale_review"
		return result, nil
	}
	result.Reason = "review"
	switch selected.State {
	case "changes_requested":
		result.NextAction, result.Reason = protocol.WorktreeChangesRequested, "changes_requested"
	case "approved":
		result.Reason = "stale_review"
		for _, event := range selected.Events {
			if event.Kind == "approve" && event.SnapshotID == selected.SnapshotID && event.ActorID != "" {
				result.NextAction, result.Reason = protocol.WorktreeMerge, "approved"
			}
		}
	case "draft", "open", "merged":
		result.Reason = "review"
	}
	if result.NextAction == protocol.WorktreeMerge {
		target, err := TargetWorktree(ctx, path, expected.Target)
		if err != nil {
			return result, err
		}
		if target != "" {
			dirty, err := git(ctx, target, "status", "--porcelain=v1", "-z", "--untracked-files=all")
			if err != nil {
				return result, err
			}
			if dirty != "" {
				result.NextAction, result.Reason = protocol.WorktreeRetained, "dirty"
			}
		}
	}
	return result, nil
}

type inventoryReceipt struct {
	root, path, key string
	branches        []string
	record          Record
}

func inventorySnapshot(ctx context.Context, input inventoryReceipt) (Snapshot, error) {
	record := input.record
	if record.VersionID != "" {
		if _, err := os.Lstat(filepath.Join(input.root, ".local-review-cache")); err != nil {
			return Snapshot{}, err
		}
		store, err := OpenBlobStore(input.root, 64<<20)
		if err != nil {
			return Snapshot{}, err
		}
		defer store.Close()
		version, err := store.LoadVersion(ctx, record.VersionID)
		if err != nil {
			return Snapshot{}, err
		}
		return version.RecoverySnapshot(record.VersionID), nil
	}
	if record.Snapshot != nil {
		return *record.Snapshot, nil
	}
	for _, target := range input.branches {
		if RecordKey(Snapshot{Path: input.path, Target: target}) == input.key {
			snapshot, err := Read(ctx, input.path, target)
			if errors.Is(err, ErrTooLarge) {
				return Snapshot{Path: input.path, Target: target}, nil
			}
			return snapshot, err
		}
	}
	return Snapshot{}, nil
}
