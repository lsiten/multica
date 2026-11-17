package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestFileMetadataCompletionRollsBackAllLocationsWhenItsSnapshotChanged(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	if _, err := testHandler.observeWorkspaceFiles(t.Context(), directory, []workspaceFileObservation{{Path: "folder/one.md"}, {Path: "folder/two.md"}}, true); err != nil {
		t.Fatal(err)
	}
	mutation := admitFileMutation(t, directory, "move-folder", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "folder", Destination: "renamed"})
	if _, err := testHandler.markWorkspaceFileDispatch(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	var resources []workspaceFileSnapshot
	if err := json.Unmarshal(mutation.admission.Resources, &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources) < 2 {
		t.Fatal("fixture requires multiple file identities")
	}
	last := resources[len(resources)-1]
	dbfx.Exec(t, "UPDATE workspace_file_resource SET relative_path='changed.md', path_key=$1 WHERE workspace_id=$2 AND id=$3", workspaceFileDigest("changed.md"), testWorkspaceID, last.ID)
	page, err := json.Marshal(protocol.WorkspaceFileOperationResult{Kind: "file_operation", CommandID: "move-folder", OperationID: mutation.command.FileOperationID, Operation: protocol.WorkspaceMove, Path: "folder", Destination: "renamed", Directory: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = testHandler.completeWorkspaceFileOperation(t.Context(), mutation, protocol.LocalReviewResult{Page: page})

	if !errors.Is(err, errWorkspaceFileSnapshotChanged) {
		t.Fatalf("a changed snapshot was committed: %v", err)
	}
	first, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: resources[0].ID})
	if err != nil || first.RelativePath != resources[0].Path {
		t.Fatalf("an earlier location escaped the rollback: %+v, %v", first, err)
	}
	record, err := testHandler.Queries.GetWorkspaceFileOperation(t.Context(), db.GetWorkspaceFileOperationParams{WorkspaceID: directory.workspaceID, ID: mutation.admission.ID})
	if err != nil || record.State != "pending" {
		t.Fatalf("partial metadata was reported as complete: %+v, %v", record, err)
	}
}

func TestWorkspaceDeletionClearsFileAdmissionsAndHostRevisions(t *testing.T) {
	workspace := dbfx.Workspace(t, "File coordination teardown", "file-coordination-"+uuid.NewString())
	fx := testutil.New(testPool, workspace, testUserID)
	fx.Member(t, workspace, testUserID, "owner")
	runtimeID := fx.Runtime(t, "file coordination owner")
	agent := fx.Agent(t, "file coordination agent", runtimeID)
	issue := fx.Issue(t, "file coordination root")
	root := "/workspace/" + workspace
	task := fx.Task(t, agent, testutil.Cols{"runtime_id": runtimeID, "issue_id": issue, "work_dir": root, "status": "completed"})
	runtime, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(runtimeID))
	if err != nil {
		t.Fatal(err)
	}
	directory := newWorkspaceFileDirectory(runtime, DocumentCommentResource{Kind: "workspace_file", TaskID: task, RuntimeID: runtimeID, WorkDir: root, OwnerKind: "issue", OwnerID: issue})
	dbfx.Cleanup(t, "DELETE FROM workspace_file_operation WHERE workspace_id=$1", workspace)
	dbfx.Cleanup(t, "DELETE FROM workspace_file_host_state WHERE workspace_id=$1", workspace)
	dbfx.Cleanup(t, "DELETE FROM workspace_file_resource WHERE workspace_id=$1", workspace)
	mutation := admitFileMutation(t, directory, "pending", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "renamed.md"})

	testutil.Call(t, testHandler.DeleteWorkspace, withURLParam(newRequest(http.MethodDelete, "/api/workspaces/"+workspace, nil), "id", workspace)).Want(http.StatusNoContent)

	var count int
	dbfx.QueryRow(t, "SELECT count(*) FROM workspace_file_operation WHERE workspace_id=$1 AND id=$2", workspace, mutation.admission.ID).Scan(&count)
	if count != 0 {
		t.Fatal("workspace deletion left its file admission behind")
	}
	dbfx.QueryRow(t, "SELECT count(*) FROM workspace_file_host_state WHERE workspace_id=$1 AND host_key=$2", workspace, directory.hostKey).Scan(&count)
	if count != 0 {
		t.Fatal("workspace deletion left its host revision behind")
	}
}
