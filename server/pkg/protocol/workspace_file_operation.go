package protocol

import (
	"errors"
	"path"
	"strings"

	"github.com/google/uuid"
)

type WorkspaceFileOperationKind string

const (
	WorkspaceCreateFile      WorkspaceFileOperationKind = "create_file"
	WorkspaceCreateDirectory WorkspaceFileOperationKind = "create_directory"
	WorkspaceMove            WorkspaceFileOperationKind = "move"
	WorkspaceTrash           WorkspaceFileOperationKind = "trash"
	WorkspaceRestore         WorkspaceFileOperationKind = "restore"
)

var ErrInvalidWorkspaceFileOperation = errors.New("invalid workspace file operation")

type WorkspaceFileOperation struct {
	Kind           WorkspaceFileOperationKind `json:"kind"`
	Path           string                     `json:"path"`
	Destination    string                     `json:"destination,omitempty"`
	Content        string                     `json:"content,omitempty"`
	RecoveryID     string                     `json:"recovery_id,omitempty"`
	ExpectedFileID string                     `json:"expected_file_id,omitempty"`
}

type WorkspaceFileOperationResult struct {
	Kind        string                     `json:"kind"`
	CommandID   string                     `json:"command_id"`
	OperationID string                     `json:"operation_id,omitempty"`
	Operation   WorkspaceFileOperationKind `json:"operation"`
	Path        string                     `json:"path"`
	Destination string                     `json:"destination,omitempty"`
	RecoveryID  string                     `json:"recovery_id,omitempty"`
	Directory   bool                       `json:"directory"`
}

func ValidWorkspaceOperationPath(value string) bool {
	if value == "" || len(value) > 4096 || value == "." || strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.ContainsAny(value, "\\\x00:") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		switch strings.ToLower(part) {
		case ".git", ".multica", ".task_owner", ".task_lock", ".review-runtime.json", ".local-review-cache":
			return false
		}
	}
	return true
}

func (operation WorkspaceFileOperation) Validate() error {
	if !ValidWorkspaceOperationPath(operation.Path) || len(operation.Content) > 256<<10 {
		return ErrInvalidWorkspaceFileOperation
	}
	if operation.ExpectedFileID != "" {
		if _, err := uuid.Parse(operation.ExpectedFileID); err != nil || len(operation.ExpectedFileID) != 36 || (operation.Kind != WorkspaceMove && operation.Kind != WorkspaceTrash) {
			return ErrInvalidWorkspaceFileOperation
		}
	}
	switch operation.Kind {
	case WorkspaceCreateFile:
		if operation.Destination != "" || operation.RecoveryID != "" {
			return ErrInvalidWorkspaceFileOperation
		}
	case WorkspaceCreateDirectory, WorkspaceTrash:
		if operation.Destination != "" || operation.RecoveryID != "" || operation.Content != "" {
			return ErrInvalidWorkspaceFileOperation
		}
	case WorkspaceMove:
		if !ValidWorkspaceOperationPath(operation.Destination) || operation.Path == operation.Destination || strings.HasPrefix(operation.Destination, operation.Path+"/") || operation.Content != "" || operation.RecoveryID != "" {
			return ErrInvalidWorkspaceFileOperation
		}
	case WorkspaceRestore:
		if operation.RecoveryID == "" || len(operation.RecoveryID) > 128 || operation.Destination != "" || operation.Content != "" {
			return ErrInvalidWorkspaceFileOperation
		}
	default:
		return ErrInvalidWorkspaceFileOperation
	}
	return nil
}
