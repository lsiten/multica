package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func workspaceFileRecoveryAlias(t *testing.T, directory workspaceFileDirectory) workspaceFileDirectory {
	t.Helper()
	source, err := testHandler.Queries.GetAgentTaskInWorkspace(t.Context(), db.GetAgentTaskInWorkspaceParams{WorkspaceID: directory.workspaceID, ID: parseUUID(directory.binding.TaskID)})
	if err != nil {
		t.Fatal(err)
	}
	issue := dbfx.Issue(t, "Another file namespace")
	task := dbfx.Task(t, uuidToString(source.AgentID), testutil.Cols{"runtime_id": directory.binding.RuntimeID, "issue_id": issue, "work_dir": directory.root, "status": "completed", "completed_at": testutil.Raw("now()")})
	ref := DocumentCommentResource{Kind: "workspace_file", TaskID: task, RuntimeID: directory.binding.RuntimeID, WorkDir: directory.root, RelativePath: "notes.md"}
	var note DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: ref, Content: "This namespace's file discussion"})).Want(http.StatusCreated).JSON(&note)
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, note.ID)
	if err := json.Unmarshal(note.Resource, &ref); err != nil {
		t.Fatal(err)
	}
	alias := newWorkspaceFileDirectory(directory.runtime, ref)
	dbfx.Cleanup(t, "DELETE FROM workspace_file_resource WHERE workspace_id=$1 AND directory_key=$2", testWorkspaceID, alias.key)
	return alias
}

func TestWorkspaceFileRecoveryRespectsOriginalLogicalOwnership(t *testing.T) {
	for _, existed := range []bool{true, false} {
		t.Run(map[bool]string{true: "affected alias", false: "new unrelated namespace"}[existed], func(t *testing.T) {
			directory, _ := workspaceFileMutationFixture(t)
			var alias workspaceFileDirectory
			if existed {
				alias = workspaceFileRecoveryAlias(t, directory)
			}
			finishFileMutation(t, admitFileMutation(t, directory, "original-delete", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "notes.md"}))
			if !existed {
				alias = workspaceFileRecoveryAlias(t, directory)
			}
			restore := protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceRestore, Path: "restored.md", RecoveryID: "original-delete"}

			_, err := testHandler.admitWorkspaceFileOperation(t.Context(), alias, protocol.LocalReviewCommand{Action: "file_operation", CommandID: "restore-alias", ActorID: testUserID, FileOperation: &restore})

			if existed && err != nil {
				t.Fatalf("an original affected namespace lost recovery: %v", err)
			}
			if !existed && !errors.Is(err, errWorkspaceFileRecoveryUnavailable) {
				t.Fatalf("a new namespace admitted another owner's deleted file: %v", err)
			}
		})
	}
}
