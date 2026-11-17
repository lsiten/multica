package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func workspaceFileMutationFixture(t *testing.T) (workspaceFileDirectory, DocumentCommentResponse) {
	t.Helper()
	ref, _ := fileCommentRuns(t)
	var note DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: ref, Content: "File identity discussion"})).Want(http.StatusCreated).JSON(&note)
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, note.ID)
	var resource DocumentCommentResource
	if err := json.Unmarshal(note.Resource, &resource); err != nil {
		t.Fatal(err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(resource.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	directory := newWorkspaceFileDirectory(runtime, resource)
	dbfx.Cleanup(t, "DELETE FROM workspace_file_host_state WHERE workspace_id=$1 AND host_key=$2", testWorkspaceID, directory.hostKey)
	dbfx.Cleanup(t, "DELETE FROM workspace_file_operation WHERE workspace_id=$1 AND host_key=$2", testWorkspaceID, directory.hostKey)
	return directory, note
}

func admitFileMutation(t *testing.T, directory workspaceFileDirectory, id string, operation protocol.WorkspaceFileOperation) workspaceFileMutation {
	t.Helper()
	command := protocol.LocalReviewCommand{Action: "file_operation", CommandID: id, ActorID: testUserID, FileOperation: &operation}
	record, err := testHandler.admitWorkspaceFileOperation(t.Context(), directory, command)
	if err != nil {
		t.Fatal(err)
	}
	command.FileOperationID = uuidToString(record.ID)
	return workspaceFileMutation{directory: directory, command: command, admission: record}
}

func finishFileMutation(t *testing.T, mutation workspaceFileMutation) protocol.LocalReviewResult {
	t.Helper()
	if _, err := testHandler.markWorkspaceFileDispatch(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	operation := mutation.command.FileOperation
	receipt := protocol.WorkspaceFileOperationResult{Kind: "file_operation", CommandID: mutation.command.CommandID, OperationID: mutation.command.FileOperationID, Operation: operation.Kind, Path: operation.Path, Destination: operation.Destination}
	if operation.Kind == protocol.WorkspaceTrash {
		receipt.RecoveryID = mutation.command.CommandID
	}
	if operation.Kind == protocol.WorkspaceRestore {
		receipt.RecoveryID = operation.RecoveryID
	}
	page, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	result, err := testHandler.completeWorkspaceFileOperation(t.Context(), mutation, protocol.LocalReviewResult{Page: page})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFileMutationReplayNeverMovesAReplacementIdentity(t *testing.T) {
	directory, note := workspaceFileMutationFixture(t)
	move := protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "renamed.md", ExpectedFileID: directory.binding.FileID}
	first := admitFileMutation(t, directory, "move-once", move)
	firstResult := finishFileMutation(t, first)
	created := admitFileMutation(t, directory, "new-generation", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "notes.md", Content: "New generation"})
	finishFileMutation(t, created)
	replay := admitFileMutation(t, directory, "move-once", move)

	replayedResult := finishFileMutation(t, replay)

	var originalPage, repeatedPage struct {
		FileID   string `json:"file_id"`
		Revision int64  `json:"directory_revision"`
	}
	if json.Unmarshal(firstResult.Page, &originalPage) != nil || json.Unmarshal(replayedResult.Page, &repeatedPage) != nil {
		t.Fatal("operation returned invalid metadata")
	}
	if replay.admission.ID != first.admission.ID || originalPage != repeatedPage {
		t.Fatal("replay returned a new identity or directory revision")
	}
	var resource DocumentCommentResource
	if err := json.Unmarshal(note.Resource, &resource); err != nil {
		t.Fatal(err)
	}
	file, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: parseUUID(resource.FileID)})
	if err != nil || file.RelativePath != "renamed.md" || file.State != "active" {
		t.Fatalf("original identity lost its location: %+v, %v", file, err)
	}
	var replacementID string
	dbfx.QueryRow(t, "SELECT id::text FROM workspace_file_resource WHERE workspace_id=$1 AND directory_key=$2 AND relative_path='notes.md' AND state='active'", testWorkspaceID, directory.key).Scan(&replacementID)
	if replacementID == resource.FileID {
		t.Fatal("the replacement inherited the moved file's identity")
	}
	changedSelection := move
	changedSelection.ExpectedFileID = replacementID
	if _, err := testHandler.admitWorkspaceFileOperation(t.Context(), directory, protocol.LocalReviewCommand{Action: "file_operation", CommandID: "move-once", ActorID: testUserID, FileOperation: &changedSelection}); !errors.Is(err, errWorkspaceFileCommandMismatch) {
		t.Fatalf("a retry changed its selected generation: %v", err)
	}
	var comments struct {
		Comments []DocumentCommentResponse `json:"comments"`
	}
	testutil.Call(t, testHandler.QueryDocumentComments, newRequest(http.MethodPost, "/query", QueryDocumentCommentsRequest{Resource: resource})).Want(http.StatusOK).JSON(&comments)
	if len(comments.Comments) != 1 || comments.Comments[0].ID != note.ID {
		t.Fatal("moving the file lost its original discussion")
	}
}

func TestFileMutationRestoresOriginalIdentityBesideAReplacement(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	deleted := admitFileMutation(t, directory, "delete-once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "notes.md"})
	finishFileMutation(t, deleted)
	created := admitFileMutation(t, directory, "replacement", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "notes.md"})
	finishFileMutation(t, created)
	restored := admitFileMutation(t, directory, "restore-once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceRestore, Path: "recovered.md", RecoveryID: "delete-once"})

	finishFileMutation(t, restored)

	var resources []workspaceFileSnapshot
	if err := json.Unmarshal(deleted.admission.Resources, &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 {
		t.Fatalf("unexpected deletion snapshot: %+v", resources)
	}
	file, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: resources[0].ID})
	if err != nil || file.RelativePath != "recovered.md" || file.State != "active" || file.RecoveryID.Valid {
		t.Fatalf("restored file lost its original identity: %+v, %v", file, err)
	}
	var count int
	dbfx.QueryRow(t, "SELECT count(*) FROM workspace_file_resource WHERE workspace_id=$1 AND directory_key=$2 AND relative_path='notes.md' AND state='active' AND id<>$3", testWorkspaceID, directory.key, file.ID).Scan(&count)
	if count != 1 {
		t.Fatal("restoration replaced the newer file identity")
	}
}

func TestFileMutationRejectsStaleDirectoryReadsAndKeepsUnknownEffectsProtected(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	reading, err := testHandler.captureWorkspaceFileRead(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	mutation := admitFileMutation(t, directory, "unknown", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "renamed.md"})
	if _, err := testHandler.markWorkspaceFileDispatch(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	if _, err := testHandler.completeWorkspaceFileOperation(t.Context(), mutation, protocol.LocalReviewResult{Error: "outcome unknown", EffectsUnknown: true}); err == nil {
		t.Fatal("unknown physical effects were treated as completion")
	}

	_, err = testHandler.observeWorkspaceFiles(t.Context(), reading, []workspaceFileObservation{{Path: "notes.md"}}, true)

	if !errors.Is(err, errWorkspaceFileDirectoryChanged) {
		t.Fatalf("stale observation was accepted: %v", err)
	}
	if _, err := testHandler.captureWorkspaceFileRead(t.Context(), directory); !errors.Is(err, errWorkspaceFileDirectoryBusy) {
		t.Fatalf("unknown effects stopped protecting the directory: %v", err)
	}
	finishFileMutation(t, mutation)
	if _, err := testHandler.captureWorkspaceFileRead(t.Context(), directory); err != nil {
		t.Fatalf("confirmed receipt did not release the directory: %v", err)
	}
}

func TestFileOperationStatusReplaysActorReceiptWithoutRuntimeRoute(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	operation := protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "status-replayed.md"}
	mutation := admitFileMutation(t, directory, "status-replay", operation)
	finishFileMutation(t, mutation)

	response := httptest.NewRecorder()
	handled, err := testHandler.tryServeWorkspaceFileOperationStatus(t.Context(), response, directory.workspaceID.String(), testUserID, mutation.command.CommandID, &operation)

	if err != nil || !handled || response.Code != http.StatusOK {
		t.Fatalf("durable receipt was not replayed: handled=%t status=%d err=%v body=%s", handled, response.Code, err, response.Body.String())
	}
	var result protocol.LocalReviewResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var receipt protocol.WorkspaceFileOperationResult
	if err := json.Unmarshal(result.Page, &receipt); err != nil || receipt.OperationID != mutation.command.FileOperationID || receipt.Destination != operation.Destination {
		t.Fatalf("replayed receipt changed identity: %+v, %v", receipt, err)
	}
}

func TestFileOperationStatusKeepsDispatchedUnknownProtected(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	operation := protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "unknown-status.md"}
	mutation := admitFileMutation(t, directory, "status-unknown", operation)
	if _, err := testHandler.markWorkspaceFileDispatch(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handled, err := testHandler.tryServeWorkspaceFileOperationStatus(t.Context(), response, directory.workspaceID.String(), testUserID, mutation.command.CommandID, &operation)

	if !handled || !errors.Is(err, errWorkspaceFileOperationUnknown) {
		t.Fatalf("dispatched unknown command was eligible for redispatch: handled=%t err=%v", handled, err)
	}
}

func TestFileIdentityReadFollowsItsMoveAndRefusesADeletedGeneration(t *testing.T) {
	directory, note := workspaceFileMutationFixture(t)
	var resource DocumentCommentResource
	if err := json.Unmarshal(note.Resource, &resource); err != nil {
		t.Fatal(err)
	}
	move := admitFileMutation(t, directory, "move", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "renamed.md"})
	finishFileMutation(t, move)
	directory.expectedFileID = resource.FileID
	directory.binding.RelativePath = "notes.md"

	reading, err := testHandler.captureWorkspaceFileRead(t.Context(), directory)

	if err != nil || reading.binding.RelativePath != "renamed.md" {
		t.Fatalf("identity did not follow its new path: %+v, %v", reading.binding, err)
	}
	deleted := admitFileMutation(t, directory, "delete", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "renamed.md"})
	finishFileMutation(t, deleted)
	if _, err := testHandler.captureWorkspaceFileRead(t.Context(), directory); !errors.Is(err, errWorkspaceFileUnavailable) {
		t.Fatalf("deleted identity was read through its old path: %v", err)
	}
}

func TestLegacyPathReadDisclosesThatAFileGenerationWasPublished(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	deleted := admitFileMutation(t, directory, "trash", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "notes.md"})
	finishFileMutation(t, deleted)
	created := admitFileMutation(t, directory, "new", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "notes.md"})
	finishFileMutation(t, created)
	directory.binding.RelativePath = "notes.md"

	reading, err := testHandler.captureWorkspaceFileRead(t.Context(), directory)

	if err != nil || !reading.identityAmbiguous {
		t.Fatalf("a legacy pointer silently adopted the replacement generation: %+v, %v", reading, err)
	}
}
