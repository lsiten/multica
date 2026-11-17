package handler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workspaceFileMutation struct {
	directory      workspaceFileDirectory
	command        protocol.LocalReviewCommand
	admission      db.WorkspaceFileOperation
	requiresUnsent bool
}

func unmarshalWorkspaceFileOperation(raw []byte, operation *protocol.WorkspaceFileOperation) error {
	if err := json.Unmarshal(raw, operation); err != nil {
		return err
	}
	return operation.Validate()
}

func completedWorkspaceFileResult(record db.WorkspaceFileOperation) (protocol.LocalReviewResult, error) {
	var result protocol.LocalReviewResult
	if record.State == "pending" {
		return result, errWorkspaceFileDirectoryBusy
	}
	if err := json.Unmarshal(record.Result, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (h *Handler) completeWorkspaceFileOperation(ctx context.Context, mutation workspaceFileMutation, result protocol.LocalReviewResult) (protocol.LocalReviewResult, error) {
	rejected := result.Error != "" && result.FileOperationRejected && !result.EffectsUnknown && len(result.Page) == 0
	var receipt protocol.WorkspaceFileOperationResult
	var err error
	if !rejected {
		receipt, err = protocol.ParseWorkspaceFileReceipt(mutation.command, result)
		if err != nil {
			return protocol.LocalReviewResult{}, err
		}
	}
	directory := mutation.directory
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	if _, err := lockWorkspaceFileHost(ctx, q, directory); err != nil {
		return protocol.LocalReviewResult{}, err
	}
	record, err := q.GetWorkspaceFileOperation(ctx, db.GetWorkspaceFileOperationParams{WorkspaceID: directory.workspaceID, ID: mutation.admission.ID})
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	if record.State != "pending" {
		return completedWorkspaceFileResult(record)
	}
	if mutation.requiresUnsent && record.DispatchStarted {
		return protocol.LocalReviewResult{}, errWorkspaceFileDirectoryBusy
	}
	if !mutation.requiresUnsent && !record.DispatchStarted {
		return protocol.LocalReviewResult{}, errWorkspaceFileSnapshotChanged
	}
	state := "rejected"
	var fileID string
	if !rejected {
		var resources []workspaceFileSnapshot
		if err := json.Unmarshal(record.Resources, &resources); err != nil {
			return protocol.LocalReviewResult{}, err
		}
		if err := applyWorkspaceFileLocations(ctx, q, mutation, resources, receipt); err != nil {
			return protocol.LocalReviewResult{}, err
		}
		if receipt.Operation == protocol.WorkspaceTrash {
			for _, resource := range resources {
				if resource.Side == "source" && resource.DirectoryKey == directory.key && resource.Path == receipt.Path {
					fileID = uuidToString(resource.ID)
				}
			}
		} else {
			selected := receipt.Path
			if receipt.Operation == protocol.WorkspaceMove {
				selected = receipt.Destination
			}
			files, err := observeWorkspaceFilesInTransaction(ctx, q, directory, []workspaceFileObservation{{Path: selected, Directory: receipt.Directory}}, true)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return protocol.LocalReviewResult{}, err
			}
			fileID = uuidToString(files[selected].ID)
		}
		state = "completed"
	}
	revision, err := q.BumpWorkspaceFileHostRevision(ctx, db.BumpWorkspaceFileHostRevisionParams{WorkspaceID: directory.workspaceID, HostKey: directory.hostKey})
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	result.ClaimToken = ""
	if !rejected {
		result.Page, err = json.Marshal(struct {
			protocol.WorkspaceFileOperationResult
			FileID            string `json:"file_id,omitempty"`
			DirectoryID       string `json:"directory_id"`
			DirectoryRevision int64  `json:"directory_revision"`
		}{receipt, fileID, directory.key, revision})
		if err != nil {
			return protocol.LocalReviewResult{}, err
		}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	updated, err := q.FinishWorkspaceFileOperation(ctx, db.FinishWorkspaceFileOperationParams{WorkspaceID: directory.workspaceID, ID: record.ID, State: state, Result: payload})
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	if updated != 1 {
		return protocol.LocalReviewResult{}, errWorkspaceFileSnapshotChanged
	}
	if err := tx.Commit(ctx); err != nil {
		return protocol.LocalReviewResult{}, err
	}
	return result, nil
}
