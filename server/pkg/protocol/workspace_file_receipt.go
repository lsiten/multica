package protocol

import (
	"encoding/json"
	"errors"
)

var ErrInvalidWorkspaceFileReceipt = errors.New("file operation receipt does not match its admission")

func ParseWorkspaceFileReceipt(command LocalReviewCommand, response LocalReviewResult) (WorkspaceFileOperationResult, error) {
	if response.Error != "" || response.EffectsUnknown || response.FileOperationRejected {
		return WorkspaceFileOperationResult{}, ErrInvalidWorkspaceFileReceipt
	}
	var wire struct {
		WorkspaceFileOperationResult
		Directory *bool `json:"directory"`
	}
	if err := json.Unmarshal(response.Page, &wire); err != nil || wire.Directory == nil {
		return WorkspaceFileOperationResult{}, ErrInvalidWorkspaceFileReceipt
	}
	result := wire.WorkspaceFileOperationResult
	result.Directory = *wire.Directory
	operation := command.FileOperation
	if command.Action != "file_operation" || operation == nil || operation.Validate() != nil || command.CommandID == "" || result.Kind != "file_operation" || result.CommandID != command.CommandID || result.OperationID != command.FileOperationID || result.Operation != operation.Kind || result.Path != operation.Path {
		return WorkspaceFileOperationResult{}, ErrInvalidWorkspaceFileReceipt
	}
	var destination, recovery string
	switch operation.Kind {
	case WorkspaceMove:
		destination = operation.Destination
	case WorkspaceTrash:
		recovery = command.CommandID
	case WorkspaceRestore:
		recovery = operation.RecoveryID
	case WorkspaceCreateFile:
		if result.Directory {
			return WorkspaceFileOperationResult{}, ErrInvalidWorkspaceFileReceipt
		}
	case WorkspaceCreateDirectory:
		if !result.Directory {
			return WorkspaceFileOperationResult{}, ErrInvalidWorkspaceFileReceipt
		}
	default:
		return WorkspaceFileOperationResult{}, ErrInvalidWorkspaceFileReceipt
	}
	if result.Destination != destination || result.RecoveryID != recovery {
		return WorkspaceFileOperationResult{}, ErrInvalidWorkspaceFileReceipt
	}
	return result, nil
}
