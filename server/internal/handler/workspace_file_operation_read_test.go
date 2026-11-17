package handler

import (
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestCompletedFileMutationCannotRegisterAStaleDirectoryPage(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	reading, err := testHandler.captureWorkspaceFileRead(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	mutation := admitFileMutation(t, directory, "move", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "renamed.md"})
	finishFileMutation(t, mutation)

	_, err = testHandler.observeWorkspaceFiles(t.Context(), reading, []workspaceFileObservation{{Path: "notes.md"}}, true)

	if !errors.Is(err, errWorkspaceFileDirectoryChanged) {
		t.Fatalf("a stale page re-registered the old path: %v", err)
	}
	var count int
	dbfx.QueryRow(t, "SELECT count(*) FROM workspace_file_resource WHERE workspace_id=$1 AND directory_key=$2 AND relative_path='notes.md' AND state='active'", testWorkspaceID, directory.key).Scan(&count)
	if count != 0 {
		t.Fatal("the moved path acquired a replacement identity from a stale response")
	}
}

func TestUnconfirmedMutationDoesNotBlockAnIndependentDirectoryOnTheSameHost(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	task, err := testHandler.Queries.GetAgentTaskInWorkspace(t.Context(), db.GetAgentTaskInWorkspaceParams{WorkspaceID: directory.workspaceID, ID: parseUUID(directory.binding.TaskID)})
	if err != nil {
		t.Fatal(err)
	}
	otherPath := directory.root + "-independent"
	otherTask := dbfx.Task(t, uuidToString(task.AgentID), testutil.Cols{"runtime_id": uuidToString(task.RuntimeID), "issue_id": uuidToString(task.IssueID), "work_dir": otherPath, "status": "completed"})
	binding := directory.binding
	binding.TaskID, binding.WorkDir = otherTask, otherPath
	independent := newWorkspaceFileDirectory(directory.runtime, binding)
	admitFileMutation(t, directory, "pending", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes.md", Destination: "renamed.md"})

	_, err = testHandler.captureWorkspaceFileRead(t.Context(), independent)

	if err != nil {
		t.Fatalf("another directory inherited the pending operation's protection: %v", err)
	}
}
