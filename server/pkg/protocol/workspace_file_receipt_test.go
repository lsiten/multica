package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestWorkspaceFileReceiptRequiresTheAdmittedOperation(t *testing.T) {
	command := LocalReviewCommand{Action: "file_operation", CommandID: "move-once", FileOperationID: "server-admission", FileOperation: &WorkspaceFileOperation{Kind: WorkspaceMove, Path: "from.md", Destination: "to.md"}}
	for _, change := range []string{"none", "command", "admission", "source", "destination", "operation", "kind", "entry type missing", "entry type null", "entry type malformed", "unknown effects", "no effects", "runtime error"} {
		t.Run(change, func(t *testing.T) {
			body := map[string]any{"kind": "file_operation", "command_id": "move-once", "operation_id": "server-admission", "operation": "move", "path": "from.md", "destination": "to.md", "directory": false}
			response := LocalReviewResult{}
			switch change {
			case "none":
			case "command":
				body["command_id"] = "another"
			case "admission":
				body["operation_id"] = "another"
			case "source":
				body["path"] = "another.md"
			case "destination":
				body["destination"] = "another.md"
			case "operation":
				body["operation"] = "trash"
			case "kind":
				body["kind"] = "another"
			case "entry type missing":
				delete(body, "directory")
			case "entry type null":
				body["directory"] = nil
			case "entry type malformed":
				body["directory"] = "false"
			case "unknown effects":
				response.EffectsUnknown = true
			case "no effects":
				response.FileOperationRejected = true
			case "runtime error":
				response.Error = "runtime refused"
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}

			response.Page = raw
			result, err := ParseWorkspaceFileReceipt(command, response)

			if change == "none" {
				if err != nil || result.OperationID != command.FileOperationID || result.Path != "from.md" {
					t.Fatalf("valid receipt rejected: %+v, %v", result, err)
				}
			} else if !errors.Is(err, ErrInvalidWorkspaceFileReceipt) {
				t.Fatalf("mismatched receipt accepted: %+v, %v", result, err)
			}
		})
	}
}

func TestWorkspaceFileReceiptMatchesRecoveryAndCreatedEntryType(t *testing.T) {
	for _, operation := range []WorkspaceFileOperation{{Kind: WorkspaceTrash, Path: "file"}, {Kind: WorkspaceRestore, Path: "file", RecoveryID: "deleted-file"}, {Kind: WorkspaceCreateFile, Path: "file"}, {Kind: WorkspaceCreateDirectory, Path: "file"}} {
		t.Run(string(operation.Kind), func(t *testing.T) {
			command := LocalReviewCommand{Action: "file_operation", CommandID: "once", FileOperation: &operation}
			result := WorkspaceFileOperationResult{Kind: "file_operation", CommandID: "once", Operation: operation.Kind, Path: "file", Directory: operation.Kind == WorkspaceCreateDirectory}
			if operation.Kind == WorkspaceTrash {
				result.RecoveryID = "once"
			}
			if operation.Kind == WorkspaceRestore {
				result.RecoveryID = operation.RecoveryID
			}
			for _, invalid := range []bool{false, true} {
				candidate := result
				if invalid {
					switch operation.Kind {
					case WorkspaceTrash, WorkspaceRestore:
						candidate.RecoveryID = "another-deletion"
					case WorkspaceCreateFile, WorkspaceCreateDirectory:
						candidate.Directory = !candidate.Directory
					}
				}
				raw, err := json.Marshal(candidate)
				if err != nil {
					t.Fatal(err)
				}

				_, err = ParseWorkspaceFileReceipt(command, LocalReviewResult{Page: raw})

				if (err != nil) != invalid {
					t.Fatalf("invalid=%t, error: %v", invalid, err)
				}
			}
		})
	}
}
