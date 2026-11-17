package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func ParseWorkspaceFileEditReceipt(command LocalReviewCommand, result LocalReviewResult) (WorkspaceFileEditResult, error) {
	var receipt WorkspaceFileEditResult
	if result.Error != "" || result.EffectsUnknown || result.FileOperationRejected || len(result.Page) != 0 || len(result.Edit) == 0 || command.FileOperationID == "" {
		return receipt, ErrInvalidWorkspaceFileReceipt
	}
	var required struct {
		Merged, Conflict *bool
		Bytes            *int64
	}
	if json.Unmarshal(result.Edit, &receipt) != nil || json.Unmarshal(result.Edit, &required) != nil || required.Merged == nil || required.Conflict == nil || required.Bytes == nil {
		return receipt, ErrInvalidWorkspaceFileReceipt
	}
	if receipt.CommandID != command.CommandID || receipt.OperationID != command.FileOperationID || receipt.FileID != command.FileID || receipt.FilePath != command.RelativePath || receipt.Bytes < 0 || len(receipt.PreviousSHA256) != 64 || receipt.NewContent != nil {
		return receipt, ErrInvalidWorkspaceFileReceipt
	}
	switch command.Action {
	case "edit":
		sum := sha256.Sum256([]byte(command.EditNew))
		if receipt.Merged || receipt.Conflict || receipt.PreviousSHA256 != command.EditBaseSHA || receipt.NewSHA256 != hex.EncodeToString(sum[:]) || receipt.Bytes != int64(len(command.EditNew)) {
			return receipt, ErrInvalidWorkspaceFileReceipt
		}
	case "merge_edit":
		if receipt.Conflict {
			if receipt.Merged || receipt.CurrentSHA256 != receipt.PreviousSHA256 || receipt.Bytes != 0 {
				return receipt, ErrInvalidWorkspaceFileReceipt
			}
		} else if !receipt.Merged || len(receipt.CurrentSHA256) != 64 {
			return receipt, ErrInvalidWorkspaceFileReceipt
		}
	default:
		return receipt, ErrInvalidWorkspaceFileReceipt
	}
	return receipt, nil
}
