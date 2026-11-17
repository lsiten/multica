package workspacefiles

import (
	"context"
	"errors"
	"os"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var (
	ErrOperationMismatch    = errors.New("file operation ID was used with different data")
	ErrOperationUnconfirmed = errors.New("file operation may have completed; refresh the directory before taking another action")
	ErrOperationRejected    = errors.New("file operation did not change the working directory")
	ErrUnsupportedEntry     = errors.New("only regular files and directories can be managed")
)

type Request struct {
	CommandID   string
	OperationID string `json:"operation_id,omitempty"`
	ActorID     string
	Operation   protocol.WorkspaceFileOperation
}

type Scope struct {
	Working *os.Root
	Storage *os.Root
}

// Apply runs under the owning environment's mutation lock. A prepared receipt
// is never replayed blindly after interruption: its filesystem effect is unknown.
func Apply(ctx context.Context, scope Scope, request Request) (protocol.WorkspaceFileOperationResult, error) {
	root := scope.Working
	var empty protocol.WorkspaceFileOperationResult
	if err := request.Operation.Validate(); err != nil {
		return empty, err
	}
	if request.CommandID == "" || len(request.CommandID) > 128 || len(request.OperationID) > 128 || request.ActorID == "" {
		return empty, protocol.ErrInvalidWorkspaceFileOperation
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	records, err := openRecords(scope.Storage)
	if err != nil {
		return empty, err
	}
	defer records.Close()
	key, hash, err := receiptIdentity(request)
	if err != nil {
		return empty, err
	}
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
			return previous.Result, nil
		case "failed":
			if request.OperationID != "" {
				return empty, errors.Join(ErrOperationRejected, errors.New(previous.Failure))
			}
		case "prepared":
			return empty, ErrOperationUnconfirmed
		default:
			return empty, ErrOperationMismatch
		}
	}
	metadata := request
	metadata.Operation.Content = ""
	receipt := operationReceipt{Hash: hash, State: "prepared", Request: metadata}
	if err := saveReceipt(records, key, receipt); err != nil {
		return empty, err
	}
	result, operationErr := perform(ctx, root, execution{request: request, records: records, key: key})
	if operationErr != nil {
		receipt.State = "failed"
		receipt.Failure = operationErr.Error()
		if err := saveReceipt(records, key, receipt); err != nil {
			return empty, errors.Join(operationErr, ErrOperationUnconfirmed, err)
		}
		return empty, errors.Join(ErrOperationRejected, operationErr)
	}
	receipt.State, receipt.Result = "completed", result
	if err := saveReceipt(records, key, receipt); err != nil {
		return empty, errors.Join(ErrOperationUnconfirmed, err)
	}
	return result, nil
}

type execution struct {
	request Request
	records *os.Root
	key     string
}

func perform(ctx context.Context, root *os.Root, execution execution) (protocol.WorkspaceFileOperationResult, error) {
	operation := execution.request.Operation
	result := protocol.WorkspaceFileOperationResult{Kind: "file_operation", CommandID: execution.request.CommandID, OperationID: execution.request.OperationID, Operation: operation.Kind, Path: operation.Path, Destination: operation.Destination}
	if err := checkParents(root, operation.Path); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	switch operation.Kind {
	case protocol.WorkspaceCreateFile:
		payload := execution.key + ".payload"
		if err := writeBytes(execution.records, payload, []byte(operation.Content)); err != nil {
			return result, errors.Join(err, execution.records.Remove(payload))
		}
		if err := moveEntry(entryMove{sourceRoot: execution.records, source: payload, destinationRoot: root, destination: operation.Path}); err != nil {
			return result, errors.Join(err, execution.records.Remove(payload))
		}
		return result, nil
	case protocol.WorkspaceCreateDirectory:
		result.Directory = true
		return result, root.Mkdir(operation.Path, 0o755)
	case protocol.WorkspaceMove, protocol.WorkspaceTrash:
		info, err := regularEntry(root, operation.Path)
		if err != nil {
			return result, err
		}
		result.Directory = info.IsDir()
		destination := operation.Destination
		destinationRoot := root
		if operation.Kind == protocol.WorkspaceTrash {
			destination, destinationRoot = execution.key+".payload", execution.records
			result.RecoveryID = execution.request.CommandID
		} else if err := checkParents(root, destination); err != nil {
			return result, err
		}
		return result, moveEntry(entryMove{sourceRoot: root, source: operation.Path, destinationRoot: destinationRoot, destination: destination})
	case protocol.WorkspaceRestore:
		key := receiptKey(operation.RecoveryID)
		original, exists, err := loadReceipt(execution.records, key)
		if err != nil {
			return result, err
		}
		if !exists || original.Request.Operation.Kind != protocol.WorkspaceTrash || (original.State != "completed" && original.State != "prepared") {
			return result, protocol.ErrInvalidWorkspaceFileOperation
		}
		payload := key + ".payload"
		info, err := regularEntry(execution.records, payload)
		if err != nil {
			return result, err
		}
		result.Directory, result.RecoveryID = info.IsDir(), operation.RecoveryID
		return result, moveEntry(entryMove{sourceRoot: execution.records, source: payload, destinationRoot: root, destination: operation.Path})
	default:
		return result, protocol.ErrInvalidWorkspaceFileOperation
	}
}
