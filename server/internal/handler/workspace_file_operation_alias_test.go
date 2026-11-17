package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestDirectoryMoveAdoptsUnopenedLegacyDiscussionsAndUpdatesOtherLogicalAliases(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	source := directory.binding
	source.RelativePath, source.FileID = "folder/child.md", ""
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	legacy := dbfx.Insert(t, "document_comment", testutil.Cols{"workspace_id": testWorkspaceID, "resource_key": "workspace_file:" + strings.Repeat("d", 64), "resource_ref": string(encoded), "content": "Unopened legacy discussion", "author_id": testUserID})
	originalTask, err := testHandler.Queries.GetAgentTaskInWorkspace(t.Context(), db.GetAgentTaskInWorkspaceParams{WorkspaceID: directory.workspaceID, ID: parseUUID(source.TaskID)})
	if err != nil {
		t.Fatal(err)
	}
	otherIssue := dbfx.Issue(t, "Other logical directory owner")
	otherTask := dbfx.Task(t, uuidToString(originalTask.AgentID), testutil.Cols{"runtime_id": source.RuntimeID, "issue_id": otherIssue, "work_dir": directory.root, "status": "completed"})
	otherRef := source
	otherRef.TaskID = otherTask
	var otherNote DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: otherRef, Content: "Another owner's discussion"})).Want(http.StatusCreated).JSON(&otherNote)
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, otherNote.ID)
	var otherResource DocumentCommentResource
	if err := json.Unmarshal(otherNote.Resource, &otherResource); err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM workspace_file_resource WHERE workspace_id=$1 AND id=$2", testWorkspaceID, otherResource.FileID)
	mutation := admitFileMutation(t, directory, "move-folder", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "folder", Destination: "renamed"})
	if _, err := testHandler.markWorkspaceFileDispatch(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	page, err := json.Marshal(protocol.WorkspaceFileOperationResult{Kind: "file_operation", CommandID: "move-folder", OperationID: mutation.command.FileOperationID, Operation: protocol.WorkspaceMove, Path: "folder", Destination: "renamed", Directory: true})
	if err != nil {
		t.Fatal(err)
	}

	result, err := testHandler.completeWorkspaceFileOperation(t.Context(), mutation, protocol.LocalReviewResult{Page: page})

	if err != nil {
		t.Fatal(err)
	}
	var adopted string
	dbfx.QueryRow(t, "SELECT COALESCE(resource_ref->>'fileId','') FROM document_comment WHERE id=$1", legacy).Scan(&adopted)
	if adopted == "" || adopted == otherResource.FileID {
		t.Fatal("legacy note did not retain an isolated stable identity")
	}
	for _, id := range []string{adopted, otherResource.FileID} {
		file, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: parseUUID(id)})
		if err != nil || file.RelativePath != "renamed/child.md" || file.State != "active" {
			t.Fatalf("logical alias was not moved coherently: %+v, %v", file, err)
		}
	}
	if bytes.Contains(result.Page, []byte(otherResource.FileID)) {
		t.Fatal("another logical owner's identity leaked into the operation response")
	}
}

func TestUnsentRefusalCannotReleaseAnAlreadyDispatchedMutation(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	mutation := admitFileMutation(t, directory, "once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "renamed.md"})
	if _, err := testHandler.markWorkspaceFileDispatch(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	mutation.requiresUnsent = true

	_, err := testHandler.completeWorkspaceFileOperation(t.Context(), mutation, protocol.LocalReviewResult{Error: "another caller's capability probe failed", FileOperationRejected: true})

	if !errors.Is(err, errWorkspaceFileDirectoryBusy) {
		t.Fatalf("a dispatched request was classified as having no effects: %v", err)
	}
	record, err := testHandler.Queries.GetWorkspaceFileOperation(t.Context(), db.GetWorkspaceFileOperationParams{WorkspaceID: directory.workspaceID, ID: mutation.admission.ID})
	if err != nil || record.State != "pending" {
		t.Fatalf("dispatch protection was removed: %+v, %v", record, err)
	}
}
