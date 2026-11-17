package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestSharedFileDiscussionLifetimeFollowsItsRootOwner(t *testing.T) {
	for _, deletingRoot := range []bool{false, true} {
		name := "child removal retains shared file"
		if deletingRoot {
			name = "root removal retires owned file"
		}
		t.Run(name, func(t *testing.T) {
			parentRef, _ := fileCommentRuns(t)
			parentTask, err := testHandler.Queries.GetAgentTaskInWorkspace(t.Context(), db.GetAgentTaskInWorkspaceParams{ID: parseUUID(parentRef.TaskID), WorkspaceID: parseUUID(testWorkspaceID)})
			if err != nil {
				t.Fatal(err)
			}
			parent, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parentTask.IssueID, WorkspaceID: parseUUID(testWorkspaceID)})
			if err != nil {
				t.Fatal(err)
			}
			parentID := uuidToString(parent.ID)
			child := dbfx.Issue(t, "shared file child", testutil.Cols{"project_id": uuidToString(parent.ProjectID), "parent_issue_id": parentID})
			selection, err := json.Marshal(map[string]any{"code_workspace": protocol.CodeWorkspaceSelection{IssueID: parentID, RuntimeID: parentRef.RuntimeID, WorkDir: parentRef.WorkDir}})
			if err != nil {
				t.Fatal(err)
			}
			task := dbfx.Task(t, uuidToString(parentTask.AgentID), testutil.Cols{"runtime_id": parentRef.RuntimeID, "issue_id": child, "work_dir": parentRef.WorkDir, "result": string(selection), "status": "completed"})
			ref := parentRef
			ref.TaskID = task
			var discussion DocumentCommentResponse
			testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: ref, Content: "Shared root discussion"})).Want(http.StatusCreated).JSON(&discussion)
			dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, discussion.ID)
			var resource DocumentCommentResource
			if err := json.Unmarshal(discussion.Resource, &resource); err != nil {
				t.Fatal(err)
			}
			otherRef, _ := fileCommentRuns(t)
			var other DocumentCommentResponse
			testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: otherRef, Content: "Another root"})).Want(http.StatusCreated).JSON(&other)
			dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, other.ID)
			var otherResource DocumentCommentResource
			if err := json.Unmarshal(other.Resource, &otherResource); err != nil {
				t.Fatal(err)
			}
			target := child
			want := 1
			if deletingRoot {
				target, want = parentID, 0
			}

			testutil.Call(t, testHandler.DeleteIssue, withURLParam(newRequest(http.MethodDelete, "/issues/"+target, nil), "id", target)).Want(http.StatusNoContent)

			var count int
			dbfx.QueryRow(t, "SELECT count(*) FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, discussion.ID).Scan(&count)
			if count != want {
				t.Fatalf("root discussion count %d, want %d", count, want)
			}
			dbfx.QueryRow(t, "SELECT count(*) FROM workspace_file_resource WHERE workspace_id=$1 AND id=$2", testWorkspaceID, resource.FileID).Scan(&count)
			if count != want {
				t.Fatalf("root file identity count %d, want %d", count, want)
			}
			dbfx.QueryRow(t, "SELECT count(*) FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, other.ID).Scan(&count)
			if count != 1 {
				t.Fatal("another owner's discussion was removed")
			}
			dbfx.QueryRow(t, "SELECT count(*) FROM workspace_file_resource WHERE workspace_id=$1 AND id=$2", testWorkspaceID, otherResource.FileID).Scan(&count)
			if count != 1 {
				t.Fatal("another owner's file identity was removed")
			}
			if !deletingRoot {
				parentRef.FileID = resource.FileID
				var page struct {
					Comments []DocumentCommentResponse `json:"comments"`
				}
				testutil.Call(t, testHandler.QueryDocumentComments, newRequest(http.MethodPost, "/query", QueryDocumentCommentsRequest{Resource: parentRef})).Want(http.StatusOK).JSON(&page)
				if len(page.Comments) != 1 || page.Comments[0].ID != discussion.ID {
					t.Fatal("surviving owner cannot read the shared discussion")
				}
			}
		})
	}
}

func TestChatDeletionRetiresOnlyItsOwnedFileResources(t *testing.T) {
	runtime := dbfx.Runtime(t, "chat file provider", testutil.Cols{"daemon_id": uuid.NewString()})
	agent := dbfx.Agent(t, "chat file owner", runtime)
	var notes []DocumentCommentResponse
	var resources []DocumentCommentResource
	var sessions []string
	for range 2 {
		session := createHandlerTestChatSession(t, agent)
		sessions = append(sessions, session)
		root := "/chat-files/" + uuid.NewString()
		task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": session, "work_dir": root, "status": "completed"})
		ref := DocumentCommentResource{Kind: "workspace_file", TaskID: task, RuntimeID: runtime, WorkDir: root, RelativePath: "notes.md"}
		var note DocumentCommentResponse
		testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: ref, Content: "Chat file note"})).Want(http.StatusCreated).JSON(&note)
		var resource DocumentCommentResource
		if err := json.Unmarshal(note.Resource, &resource); err != nil {
			t.Fatal(err)
		}
		notes, resources = append(notes, note), append(resources, resource)
		dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, note.ID)
		dbfx.Cleanup(t, "DELETE FROM workspace_file_resource WHERE workspace_id=$1 AND id=$2", testWorkspaceID, resource.FileID)
	}

	testutil.Call(t, testHandler.DeleteChatSession, withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodDelete, "/chat/"+sessions[0], nil), "sessionId", sessions[0]))).Want(http.StatusNoContent)

	for index, want := range []int{0, 1} {
		var count int
		dbfx.QueryRow(t, "SELECT count(*) FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, notes[index].ID).Scan(&count)
		if count != want {
			t.Fatalf("chat %d note count %d, want %d", index, count, want)
		}
		dbfx.QueryRow(t, "SELECT count(*) FROM workspace_file_resource WHERE workspace_id=$1 AND id=$2", testWorkspaceID, resources[index].FileID).Scan(&count)
		if count != want {
			t.Fatalf("chat %d file identity count %d, want %d", index, count, want)
		}
	}
}
