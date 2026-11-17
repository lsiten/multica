package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func fileCommentRuns(t *testing.T) (DocumentCommentResource, DocumentCommentResource) {
	t.Helper()
	project := dbfx.Project(t, "file discussion scopes")
	issue := dbfx.Issue(t, "File discussion across runs", testutil.Cols{"project_id": project})
	host := uuid.NewString()
	root := "/file-discussions/" + uuid.NewString()
	resources := make([]DocumentCommentResource, 0, 2)
	for _, provider := range []string{"codex", "pi"} {
		working := root
		if provider == "pi" {
			working += "/"
		}
		runtime := dbfx.Runtime(t, "file discussion "+provider, testutil.Cols{"daemon_id": host, "provider": provider})
		agent := dbfx.Agent(t, "file discussion "+provider+" "+host, runtime)
		task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "work_dir": working, "status": "completed", "completed_at": testutil.Raw("now()")})
		resources = append(resources, DocumentCommentResource{Kind: "workspace_file", TaskID: task, RuntimeID: runtime, WorkDir: working, RelativePath: "notes.md"})
	}
	dbfx.Cleanup(t, "DELETE FROM workspace_file_resource WHERE workspace_id=$1 AND rtrim(binding_ref->>'workDir','/')=$2", testWorkspaceID, root)
	return resources[0], resources[1]
}

func TestLegacyFileDiscussionsRemainReadableAcrossRunsAndProviders(t *testing.T) {
	original, next := fileCommentRuns(t)
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	key := "workspace_file:" + strings.Repeat("a", 64)
	root := dbfx.Insert(t, "document_comment", testutil.Cols{"workspace_id": testWorkspaceID, "resource_key": key, "resource_ref": string(encoded), "content": "Original discussion", "author_id": testUserID, "anchor": `{"quote":"Original passage","start":5,"prefix":"","suffix":""}`})
	reply := dbfx.Insert(t, "document_comment", testutil.Cols{"workspace_id": testWorkspaceID, "resource_key": key, "resource_ref": string(encoded), "content": "Original reply", "author_id": testUserID, "parent_id": root})
	var page struct {
		Comments []DocumentCommentResponse `json:"comments"`
	}

	testutil.Call(t, testHandler.QueryDocumentComments, newRequest(http.MethodPost, "/query", QueryDocumentCommentsRequest{Resource: next})).Want(http.StatusOK).JSON(&page)

	if len(page.Comments) != 2 || page.Comments[0].ID != root || page.Comments[1].ID != reply || page.Comments[1].ParentID == nil || *page.Comments[1].ParentID != root {
		t.Fatalf("legacy thread lost its identity: %+v", page.Comments)
	}
	var resource DocumentCommentResource
	if err := json.Unmarshal(page.Comments[0].Resource, &resource); err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(resource.FileID); err != nil {
		t.Fatalf("discussion has no stable file identity: %+v", resource)
	}
	var ownerID string
	dbfx.QueryRow(t, "SELECT issue_id::text FROM agent_task_queue WHERE id=$1", original.TaskID).Scan(&ownerID)
	if resource.OwnerKind != "issue" || resource.OwnerID != ownerID {
		t.Fatalf("legacy discussion lost its lifetime owner: %+v", resource)
	}
	if page.Comments[0].AuthorID != testUserID || page.Comments[0].Content != "Original discussion" || page.Comments[0].Revision != 1 {
		t.Fatalf("identity adoption rewrote the discussion: %+v", page.Comments[0])
	}
	var anchor DocumentCommentAnchor
	if err := json.Unmarshal(page.Comments[0].Anchor, &anchor); err != nil {
		t.Fatal(err)
	}
	if anchor.Quote != "Original passage" || anchor.Start != 5 {
		t.Fatalf("identity adoption rewrote the source: %+v", anchor)
	}
}

func TestFileReplyUsesTheSameIdentityAfterProjectTransferAndAnotherRun(t *testing.T) {
	original, next := fileCommentRuns(t)
	var root DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: original, Content: "Root"})).Want(http.StatusCreated).JSON(&root)
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, root.ID)
	var originalRef DocumentCommentResource
	if err := json.Unmarshal(root.Resource, &originalRef); err != nil {
		t.Fatal(err)
	}
	next.FileID, next.RelativePath = originalRef.FileID, ""
	project := dbfx.Project(t, "file owner's next project")
	dbfx.Exec(t, "UPDATE issue SET project_id=$1 WHERE id=(SELECT issue_id FROM agent_task_queue WHERE id=$2)", project, next.TaskID)
	var reply DocumentCommentResponse

	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: next, ParentID: &root.ID, Content: "Reply from the next run"})).Want(http.StatusCreated).JSON(&reply)

	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, reply.ID)
	if reply.ParentID == nil || *reply.ParentID != root.ID {
		t.Fatalf("another run could not continue the file discussion: %+v", reply)
	}
	var replyRef DocumentCommentResource
	if err := json.Unmarshal(reply.Resource, &replyRef); err != nil {
		t.Fatal(err)
	}
	if originalRef.FileID == "" || originalRef.FileID != replyRef.FileID {
		t.Fatalf("another run forked the file discussion: %+v, %+v", originalRef, replyRef)
	}
}

func TestFileIdentityDoesNotBypassAnotherProjectScope(t *testing.T) {
	original, _ := fileCommentRuns(t)
	var root DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/notes", CreateDocumentCommentRequest{Resource: original, Content: "Scoped discussion"})).Want(http.StatusCreated).JSON(&root)
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id=$1 AND id=$2", testWorkspaceID, root.ID)
	var registered DocumentCommentResource
	if err := json.Unmarshal(root.Resource, &registered); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE project SET visibility='private' WHERE id=$1", registered.ProjectID)
	dbfx.Exec(t, "UPDATE agent_runtime SET visibility='public' WHERE id=$1", original.RuntimeID)
	viewer := createPlainMember(t, "file-identity-"+uuid.NewString()+"@example.test")
	project := dbfx.Project(t, "another file discussion scope")
	issue := dbfx.Issue(t, "Another file owner", testutil.Cols{"project_id": project})
	agent := dbfx.Agent(t, "another file discussion owner", original.RuntimeID)
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": original.RuntimeID, "issue_id": issue, "work_dir": original.WorkDir, "status": "completed"})
	forged := DocumentCommentResource{Kind: "workspace_file", TaskID: task, RuntimeID: original.RuntimeID, WorkDir: original.WorkDir, RelativePath: original.RelativePath, FileID: registered.FileID, ProjectID: registered.ProjectID}
	forged.OwnerKind, forged.OwnerID = registered.OwnerKind, registered.OwnerID
	read := middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.QueryDocumentComments))
	public := forged
	public.FileID, public.ProjectID = "", ""
	var page struct {
		Comments []DocumentCommentResponse `json:"comments"`
	}
	testutil.Call(t, read.ServeHTTP, newRequestAsUser(viewer, http.MethodPost, "/query", QueryDocumentCommentsRequest{Resource: public})).Want(http.StatusOK).JSON(&page)
	if len(page.Comments) != 0 {
		t.Fatalf("public directory inherited a private project's file discussion: %+v", page.Comments)
	}

	testutil.Call(t, read.ServeHTTP, newRequestAsUser(viewer, http.MethodPost, "/query", QueryDocumentCommentsRequest{Resource: forged})).Want(http.StatusNotFound)
}
