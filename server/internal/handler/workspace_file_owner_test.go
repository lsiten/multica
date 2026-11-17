package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestSharedFileDirectoryRetainsItsPrivateRootAuthorityAfterChildReparent(t *testing.T) {
	original, _ := fileCommentRuns(t)
	var root DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: original, Content: "Private root's discussion"})).Want(http.StatusCreated).JSON(&root)
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, root.ID)
	var registered DocumentCommentResource
	if err := json.Unmarshal(root.Resource, &registered); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE project SET visibility='private' WHERE id=$1", registered.ProjectID)
	dbfx.Exec(t, "UPDATE agent_runtime SET visibility='public' WHERE id=$1", original.RuntimeID)
	child := dbfx.Issue(t, "formerly shared child", testutil.Cols{"project_id": registered.ProjectID, "parent_issue_id": registered.OwnerID})
	agent := dbfx.Agent(t, "shared directory provider", original.RuntimeID)
	selection, err := json.Marshal(map[string]any{"code_workspace": protocol.CodeWorkspaceSelection{IssueID: registered.OwnerID, RuntimeID: original.RuntimeID, WorkDir: original.WorkDir}})
	if err != nil {
		t.Fatal(err)
	}
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": original.RuntimeID, "issue_id": child, "work_dir": original.WorkDir, "result": string(selection), "status": "completed"})
	// The admitted checkout keeps its owner after the child changes ancestry.
	project := dbfx.Project(t, "child's public destination")
	dbfx.Exec(t, "UPDATE issue SET project_id=$1,parent_issue_id=NULL WHERE id=$2", project, child)
	viewer := createPlainMember(t, "file-owner-"+uuid.NewString()+"@example.test")
	resource := DocumentCommentResource{Kind: "workspace_file", TaskID: task, RuntimeID: original.RuntimeID, WorkDir: original.WorkDir, FileID: registered.FileID, RelativePath: "notes.md"}
	for _, tc := range []struct {
		name   string
		handle http.HandlerFunc
		body   any
	}{
		{"discussion", testHandler.QueryDocumentComments, QueryDocumentCommentsRequest{Resource: resource}},
		{"filesystem", testHandler.ForwardLocalReview, protocol.LocalReviewCommand{TaskID: task, RuntimeID: original.RuntimeID, Path: original.WorkDir, Action: "workspace_files", RelativePath: ".", Limit: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRequestAsUser(viewer, http.MethodPost, "/query", tc.body)
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			read := middleware.RequireWorkspaceMember(testHandler.Queries)(tc.handle)
			testutil.Call(t, read.ServeHTTP, r.WithContext(ctx)).Want(http.StatusNotFound)
		})
	}
}
