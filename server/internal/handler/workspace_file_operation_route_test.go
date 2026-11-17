package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func removeFileDirectoryRuntime(t *testing.T, directory workspaceFileDirectory) {
	t.Helper()
	task, err := testHandler.Queries.GetAgentTaskInWorkspace(t.Context(), db.GetAgentTaskInWorkspaceParams{WorkspaceID: directory.workspaceID, ID: parseUUID(directory.binding.TaskID)})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE agent SET archived_at=now(), archived_by=$1 WHERE id=$2", testUserID, task.AgentID)
	testutil.Call(t, testHandler.DeleteAgentRuntime, withURLParam(newRequest(http.MethodDelete, "/api/runtimes/"+directory.binding.RuntimeID, nil), "runtimeId", directory.binding.RuntimeID)).Want(http.StatusOK)
}

func TestFileMutationUsesPersistedHostIdentityAfterItsOldRuntimeIsDeleted(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	files, err := testHandler.observeWorkspaceFiles(t.Context(), directory, []workspaceFileObservation{{Path: "folder/child.md"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	childID := files["folder/child.md"].ID
	otherRuntime := dbfx.Runtime(t, "unrelated file host", testutil.Cols{"daemon_id": uuid.NewString()})
	otherAgent := dbfx.Agent(t, "unrelated file host agent", otherRuntime)
	otherIssue := dbfx.Issue(t, "unrelated file host root")
	otherTask := dbfx.Task(t, otherAgent, testutil.Cols{"runtime_id": otherRuntime, "issue_id": otherIssue, "work_dir": directory.root, "status": "completed", "completed_at": testutil.Raw("now()")})
	otherRef := DocumentCommentResource{Kind: "workspace_file", TaskID: otherTask, RuntimeID: otherRuntime, WorkDir: directory.root, RelativePath: "folder/child.md"}
	var otherNote DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: otherRef, Content: "Different machine, same path"})).Want(http.StatusCreated).JSON(&otherNote)
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, otherNote.ID)
	if err := json.Unmarshal(otherNote.Resource, &otherRef); err != nil {
		t.Fatal(err)
	}
	otherHost, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(otherRuntime))
	if err != nil {
		t.Fatal(err)
	}
	other := newWorkspaceFileDirectory(otherHost, otherRef)
	dbfx.Cleanup(t, "DELETE FROM workspace_file_host_state WHERE workspace_id=$1 AND host_key=$2", testWorkspaceID, other.hostKey)
	encoded, err := json.Marshal(otherRef)
	if err != nil {
		t.Fatal(err)
	}
	// A full page of older UUIDs makes keyset progress independent of which
	// routing candidates the namespace check accepts.
	for index := 1; index <= 500; index++ {
		name := fmt.Sprintf("folder/unrelated-%03d.md", index)
		dbfx.Insert(t, "workspace_file_resource", testutil.Cols{"id": fmt.Sprintf("00000000-0000-1000-8000-%012x", index), "workspace_id": testWorkspaceID, "directory_key": other.key, "path_key": workspaceFileDigest(name), "relative_path": name, "directory": false, "binding_ref": string(encoded)})
	}
	removeFileDirectoryRuntime(t, directory)
	removeFileDirectoryRuntime(t, other)
	peerRuntime := dbfx.Runtime(t, "new file provider", testutil.Cols{"daemon_id": directory.runtime.DaemonID.String})
	peerAgent := dbfx.Agent(t, "new file provider agent", peerRuntime)
	peerTask := dbfx.Task(t, peerAgent, testutil.Cols{"runtime_id": peerRuntime, "issue_id": directory.binding.OwnerID, "work_dir": directory.root, "status": "completed", "completed_at": testutil.Raw("now()")})
	peer, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(peerRuntime))
	if err != nil {
		t.Fatal(err)
	}
	binding := directory.binding
	binding.RuntimeID, binding.TaskID = peerRuntime, peerTask
	current := newWorkspaceFileDirectory(peer, binding)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	operation := protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "folder", Destination: "renamed"}
	command := protocol.LocalReviewCommand{Action: "file_operation", CommandID: "move-folder", ActorID: testUserID, FileOperation: &operation}
	record, err := testHandler.admitWorkspaceFileOperation(ctx, current, command)
	if err != nil {
		t.Fatal(err)
	}
	command.FileOperationID = uuidToString(record.ID)
	mutation := workspaceFileMutation{directory: current, command: command, admission: record}
	if _, err := testHandler.markWorkspaceFileDispatch(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	page, err := json.Marshal(protocol.WorkspaceFileOperationResult{Kind: "file_operation", CommandID: "move-folder", OperationID: mutation.command.FileOperationID, Operation: protocol.WorkspaceMove, Path: "folder", Destination: "renamed", Directory: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := testHandler.completeWorkspaceFileOperation(t.Context(), mutation, protocol.LocalReviewResult{Page: page}); err != nil {
		t.Fatal(err)
	}

	moved, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: childID})
	if err != nil || moved.RelativePath != "renamed/child.md" {
		t.Fatalf("removing the old route lost a retained file identity: %+v, %v", moved, err)
	}
	unrelated, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: parseUUID(otherRef.FileID)})
	if err != nil || unrelated.RelativePath != "folder/child.md" {
		t.Fatalf("another physical host's same-name file was moved: %+v, %v", unrelated, err)
	}
}

func TestFileOperationRetryReplaysReceiptAfterItsRuntimeRouteDisappears(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	operation := protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "route-gone.md"}
	mutation := admitFileMutation(t, directory, "route-gone-retry", operation)
	finishFileMutation(t, mutation)
	removeFileDirectoryRuntime(t, directory)

	request := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", protocol.LocalReviewCommand{
		TaskID: directory.binding.TaskID, RuntimeID: directory.binding.RuntimeID, WorkspaceID: directory.workspaceID.String(), Path: directory.root,
		Action: "file_operation", CommandID: mutation.command.CommandID, ActorID: testUserID, FileOperation: &operation,
	}))
	var result protocol.LocalReviewResult
	testutil.Call(t, testHandler.ForwardLocalReview, request).Want(http.StatusOK).JSON(&result)
	var receipt protocol.WorkspaceFileOperationResult
	if err := json.Unmarshal(result.Page, &receipt); err != nil || receipt.OperationID != mutation.command.FileOperationID || receipt.Destination != operation.Destination {
		t.Fatalf("old route retry did not replay the durable receipt: %+v, %v", receipt, err)
	}
}
