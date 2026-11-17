package workspacefiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type EditRequest struct {
	CommandID, OperationID, FileID, Action, Path string
	BaseSHA, Base, New                           string
}

// ApplyEdit shares the environment mutation lock and private journal with file
// movement. A prepared edit is never executed again after an uncertain write.
func ApplyEdit(ctx context.Context, scope Scope, request EditRequest) (protocol.WorkspaceFileEditResult, error) {
	var empty protocol.WorkspaceFileEditResult
	if request.CommandID == "" || len(request.CommandID) > 128 || request.OperationID == "" || len(request.OperationID) > 128 || request.FileID == "" || !protocol.ValidWorkspaceOperationPath(request.Path) || (request.Action != "edit" && request.Action != "merge_edit") {
		return empty, protocol.ErrInvalidWorkspaceFileOperation
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return empty, err
	}
	sum := sha256.Sum256(data)
	hash, key := hex.EncodeToString(sum[:]), "edit-"+receiptKey(request.CommandID)
	records, err := openRecords(scope.Storage)
	if err != nil {
		return empty, err
	}
	defer records.Close()
	previous, exists, err := loadReceipt(records, key)
	if err != nil {
		return empty, err
	}
	if exists {
		if previous.Hash != hash {
			return empty, ErrOperationMismatch
		}
		switch previous.State {
		case "completed":
			if previous.Edit == nil {
				return empty, ErrOperationMismatch
			}
			return *previous.Edit, nil
		case "failed":
			return empty, errors.Join(ErrOperationRejected, errors.New(previous.Failure))
		case "prepared":
			return empty, ErrOperationUnconfirmed
		default:
			return empty, ErrOperationMismatch
		}
	}
	record := operationReceipt{Hash: hash, State: "prepared"}
	if err := saveReceipt(records, key, record); err != nil {
		return empty, err
	}
	result, editErr := performEdit(ctx, scope, request)
	if editErr != nil {
		record.State, record.Failure = "failed", editErr.Error()
		if err := saveReceipt(records, key, record); err != nil {
			return empty, errors.Join(editErr, ErrOperationUnconfirmed, err)
		}
		return empty, errors.Join(ErrOperationRejected, editErr)
	}
	record.State, record.Edit = "completed", &result
	if err := saveReceipt(records, key, record); err != nil {
		return empty, errors.Join(ErrOperationUnconfirmed, err)
	}
	return result, nil
}

func performEdit(ctx context.Context, scope Scope, request EditRequest) (protocol.WorkspaceFileEditResult, error) {
	result := protocol.WorkspaceFileEditResult{CommandID: request.CommandID, OperationID: request.OperationID, FileID: request.FileID, FilePath: request.Path}
	if err := checkParents(scope.Working, request.Path); err != nil {
		return result, err
	}
	switch request.Action {
	case "edit":
		applied, err := localreview.ApplyWorkingFileEdit(ctx, scope.Working, localreview.EditWorkingFileRequest{FilePath: request.Path, BaseSHA256: request.BaseSHA, NewContent: []byte(request.New)})
		result.PreviousSHA256, result.NewSHA256, result.Bytes = applied.PreviousSHA256, applied.NewSHA256, applied.Bytes
		return result, err
	case "merge_edit":
		merged, err := localreview.MergeWorkingFile(ctx, scope.Working, localreview.MergeWorkingFileRequest{FilePath: request.Path, Base: []byte(request.Base), New: []byte(request.New)})
		result.Merged, result.Conflict, result.Bytes = merged.Merged, merged.Conflict, merged.Bytes
		result.PreviousSHA256, result.CurrentSHA256 = merged.PreviousSHA256, merged.CurrentSHA256
		return result, err
	default:
		return result, protocol.ErrInvalidWorkspaceFileOperation
	}
}
