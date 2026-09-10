package localreview

import (
	"context"
	"errors"
)

var ErrIndexRecoveryRequired = errors.New("previous Git operation cannot be confirmed; refresh status before starting a new operation")

// ExecuteIndexOperation requires the owning daemon's task/repository locks.
// Replay never applies a new Git mutation: it returns a completed receipt, or
// proves a prepared result is already present before completing that receipt.
func (s *BlobStore) ExecuteIndexOperation(ctx context.Context, root string, request IndexOperationRequest) (IndexOperationResult, error) {
	receipt, exists, err := LoadIndexReceipt(root, request)
	if err != nil {
		return IndexOperationResult{}, err
	}
	if exists {
		if receipt.State == "completed" {
			return receipt.Result, nil
		}
		if err := confirmIndexOperation(ctx, request, receipt.Result); err != nil {
			return IndexOperationResult{}, err
		}
		if err := CompleteIndexReceipt(root, request); err != nil {
			return IndexOperationResult{}, err
		}
		return receipt.Result, nil
	}
	version, err := s.LoadVersion(ctx, request.VersionID)
	if err != nil {
		return IndexOperationResult{}, err
	}
	if version.Header.Repository != request.Path || version.Header.Committed || version.Header.Branch != request.Branch || version.Header.Target != request.Branch || version.Header.Head != request.Head {
		return IndexOperationResult{}, ErrIndexStateChanged
	}
	var result IndexOperationResult
	switch request.Action {
	case "stage", "unstage":
		selection := IndexSelection{Version: VersionSelection{ID: request.VersionID, Path: request.Path, Target: request.Branch}, IndexID: request.IndexID, Paths: request.Paths, Unstage: request.Action == "unstage"}
		err = s.ChangeStagingPrepared(ctx, selection, func(indexID string) error {
			result.IndexID = indexID
			return PrepareIndexReceipt(root, request, result)
		})
	case "commit":
		_, err = CommitIndexPrepared(ctx, IndexCommitRequest{Path: request.Path, Branch: request.Branch, Head: request.Head, IndexID: request.IndexID, Message: request.Message}, func(commit PreparedIndexCommit) error {
			result.Commit = &commit
			return PrepareIndexReceipt(root, request, result)
		})
	default:
		return IndexOperationResult{}, ErrInvalidIndexStatus
	}
	if err != nil {
		return IndexOperationResult{}, err
	}
	if err := CompleteIndexReceipt(root, request); err != nil {
		return IndexOperationResult{}, err
	}
	return result, nil
}

func confirmIndexOperation(ctx context.Context, request IndexOperationRequest, result IndexOperationResult) error {
	if request.Action == "commit" {
		if result.Commit == nil {
			return ErrIndexRecoveryRequired
		}
		// A subsequent commit does not invalidate proof that this prepared
		// commit already reached the selected branch's history.
		if _, err := git(ctx, request.Path, "merge-base", "--is-ancestor", result.Commit.Commit, "refs/heads/"+request.Branch); err != nil {
			return errors.Join(ErrIndexRecoveryRequired, err)
		}
		return nil
	}
	indexID, err := indexIdentity(ctx, request.Path)
	if err != nil {
		return err
	}
	if indexID != result.IndexID {
		return ErrIndexRecoveryRequired
	}
	return nil
}
