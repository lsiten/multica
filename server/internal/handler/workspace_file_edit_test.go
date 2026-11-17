package handler

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestIdentityEditProtectsItsDirectoryUntilItsOriginalReceiptIsConfirmed(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	command := protocol.LocalReviewCommand{Action: "merge_edit", CommandID: "save-once", ActorID: testUserID, FileID: directory.binding.FileID, RelativePath: "stale-path.md", EditBase: "original", EditNew: "submitted"}
	record, err := testHandler.admitWorkspaceFileEdit(t.Context(), directory, command)
	if err != nil {
		t.Fatal(err)
	}
	var intent workspaceFileEditIntent
	if err := json.Unmarshal(record.Operation, &intent); err != nil {
		t.Fatal(err)
	}
	if intent.Path != "notes.md" {
		t.Fatalf("edit selected the supplied stale path: %+v", intent)
	}
	if strings.Contains(string(record.Operation), "submitted") || strings.Contains(string(record.Operation), "original") {
		t.Fatal("edit contents entered persisted operation metadata")
	}
	move := protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "moved.md"}
	if _, err := testHandler.admitWorkspaceFileOperation(t.Context(), directory, protocol.LocalReviewCommand{Action: "file_operation", CommandID: "move", ActorID: testUserID, FileOperation: &move}); !errors.Is(err, errWorkspaceFileDirectoryBusy) {
		t.Fatalf("an unresolved edit allowed file movement: %v", err)
	}
	command.RelativePath, command.FileOperationID = intent.Path, uuidToString(record.ID)
	mutation := workspaceFileMutation{directory: directory, command: command, admission: record}
	if _, err := testHandler.markWorkspaceFileDispatch(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	if _, err := testHandler.completeWorkspaceFileEdit(t.Context(), mutation, protocol.LocalReviewResult{Error: "lost response", EffectsUnknown: true}); !errors.Is(err, protocol.ErrInvalidWorkspaceFileReceipt) {
		t.Fatalf("unknown edit was acknowledged: %v", err)
	}
	receipt, err := json.Marshal(protocol.WorkspaceFileEditResult{CommandID: command.CommandID, OperationID: command.FileOperationID, FileID: command.FileID, FilePath: "notes.md", Merged: true, PreviousSHA256: strings.Repeat("a", 64), CurrentSHA256: strings.Repeat("b", 64), Bytes: 9})
	if err != nil {
		t.Fatal(err)
	}

	first, err := testHandler.completeWorkspaceFileEdit(t.Context(), mutation, protocol.LocalReviewResult{Edit: receipt})
	if err != nil {
		t.Fatal(err)
	}
	finishFileMutation(t, admitFileMutation(t, directory, "move", move))
	finishFileMutation(t, admitFileMutation(t, directory, "replacement", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "notes.md"}))
	replayed, err := testHandler.admitWorkspaceFileEdit(t.Context(), directory, command)
	if err != nil {
		t.Fatal(err)
	}
	result, err := completedWorkspaceFileResult(replayed)
	var initialReceipt, replayedReceipt protocol.WorkspaceFileEditResult
	if json.Unmarshal(first.Edit, &initialReceipt) != nil || json.Unmarshal(result.Edit, &replayedReceipt) != nil {
		t.Fatal("invalid edit receipt")
	}
	if err != nil || initialReceipt != replayedReceipt || replayed.ID != record.ID {
		t.Fatalf("retry lost the original edit receipt: %+v, %v", result, err)
	}
	file, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: parseUUID(command.FileID)})
	if err != nil || file.RelativePath != "moved.md" {
		t.Fatalf("edit retry affected the replacement: %+v, %v", file, err)
	}
}
