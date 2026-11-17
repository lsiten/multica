package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestDocumentCommentThreadLifecycle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "Located document discussion")
	resource := DocumentCommentResource{Kind: "issue", ID: issueID}
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id = $1 AND resource_key = $2", testWorkspaceID, "issue:"+issueID)
	anchor := &DocumentCommentAnchor{Quote: "Original passage", Start: 12, Prefix: "Introduction", Suffix: "Conclusion", Revision: pInt64(1)}
	var root DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/api/document-comments", CreateDocumentCommentRequest{Resource: resource, Content: "Clarify this paragraph", Anchor: anchor})).Want(http.StatusCreated).JSON(&root)
	if root.ParentID != nil || root.Revision != 1 {
		t.Fatalf("unexpected initial discussion: %#v", root)
	}

	var resolved DocumentCommentResponse
	resolution := ResolveDocumentCommentRequest{Resource: resource, Resolved: pBool(true), ExpectedRevision: 1}
	testutil.Call(t, testHandler.UpdateDocumentCommentResolution, withURLParam(newRequest(http.MethodPatch, "/api/document-comments/"+root.ID, resolution), "id", root.ID)).Want(http.StatusOK).JSON(&resolved)
	if resolved.ResolvedAt == nil || resolved.Revision != 2 {
		t.Fatalf("discussion was not resolved: %#v", resolved)
	}
	testutil.Call(t, testHandler.UpdateDocumentCommentResolution, withURLParam(newRequest(http.MethodPatch, "/api/document-comments/"+root.ID, resolution), "id", root.ID)).Want(http.StatusConflict)

	var reply DocumentCommentResponse
	submissionID := uuid.NewString()
	replyInput := CreateDocumentCommentRequest{SubmissionID: &submissionID, Resource: resource, Content: "The paragraph still needs context", ParentID: &root.ID}
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/api/document-comments", replyInput)).Want(http.StatusCreated).JSON(&reply)
	if reply.ParentID == nil || *reply.ParentID != root.ID {
		t.Fatalf("reply lost its thread: %#v", reply)
	}

	var page struct {
		Comments []DocumentCommentResponse `json:"comments"`
	}
	testutil.Call(t, testHandler.QueryDocumentComments, newRequest(http.MethodPost, "/api/document-comments/query", QueryDocumentCommentsRequest{Resource: resource})).Want(http.StatusOK).JSON(&page)
	if len(page.Comments) != 2 || page.Comments[0].ID != root.ID || page.Comments[0].ResolvedAt != nil || page.Comments[0].Revision != 3 {
		t.Fatalf("reply did not durably reopen the discussion: %#v", page.Comments)
	}
	var savedAnchor DocumentCommentAnchor
	if err := json.Unmarshal(page.Comments[0].Anchor, &savedAnchor); err != nil {
		t.Fatal(err)
	}
	if savedAnchor.Quote != anchor.Quote || savedAnchor.Start != 12 || savedAnchor.Revision == nil || *savedAnchor.Revision != 1 {
		t.Fatalf("original selection snapshot changed: %#v", savedAnchor)
	}
	resolution.ExpectedRevision = 3
	testutil.Call(t, testHandler.UpdateDocumentCommentResolution, withURLParam(newRequest(http.MethodPatch, "/api/document-comments/"+root.ID, resolution), "id", root.ID)).Want(http.StatusOK)
	var replay DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/api/document-comments", replyInput)).Want(http.StatusOK).JSON(&replay)
	if replay.ID != reply.ID {
		t.Fatalf("retry created another reply: %s != %s", replay.ID, reply.ID)
	}
	testutil.Call(t, testHandler.QueryDocumentComments, newRequest(http.MethodPost, "/api/document-comments/query", QueryDocumentCommentsRequest{Resource: resource})).Want(http.StatusOK).JSON(&page)
	if len(page.Comments) != 2 || page.Comments[0].ResolvedAt == nil || page.Comments[0].Revision != 4 {
		t.Fatalf("an old reply retry reopened or duplicated the discussion: %#v", page.Comments)
	}
	replyInput.Content = "Different text using the same submission id"
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/api/document-comments", replyInput)).Want(http.StatusConflict)
}

func TestDocumentCommentsDeletedWithSource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, kind := range []string{"issue", "attachment", "project_resource", "project"} {
		t.Run(kind, func(t *testing.T) {
			var ownerID string
			var remove func()
			resource := DocumentCommentResource{Kind: kind}
			switch kind {
			case "issue":
				ownerID = dbfx.Issue(t, "Document discussion source")
				resource.ID = ownerID
				remove = func() {
					testutil.Call(t, testHandler.DeleteIssue, withURLParam(newRequest(http.MethodDelete, "/api/issues/"+ownerID, nil), "id", ownerID)).Want(http.StatusNoContent)
				}
			case "attachment":
				ownerID = dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": testUserID, "filename": "discussed.md", "url": "/uploads/discussed.md", "content_type": "text/markdown", "size_bytes": 10})
				resource.ID = ownerID
				remove = func() {
					testutil.Call(t, testHandler.DeleteAttachment, withURLParam(newRequest(http.MethodDelete, "/api/attachments/"+ownerID, nil), "id", ownerID)).Want(http.StatusNoContent)
				}
			case "project_resource", "project":
				projectID := dbfx.Project(t, "Document discussion project")
				ownerID = dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": projectID, "resource_type": "local_directory", "resource_ref": `{"daemon_id":"fixture","local_path":"/workspace"}`, "label": "Workspace"})
				resource = DocumentCommentResource{Kind: "workspace_file", ProjectResourceID: ownerID, WorkDir: "/workspace", RelativePath: "proposal.md"}
				remove = func() {
					testutil.Call(t, testHandler.DeleteProjectResource, withURLParams(newRequest(http.MethodDelete, "/api/projects/"+projectID+"/resources/"+ownerID, nil), "id", projectID, "resourceId", ownerID)).Want(http.StatusNoContent)
				}
				if kind == "project" {
					remove = func() {
						testutil.Call(t, testHandler.DeleteProject, withURLParam(newRequest(http.MethodDelete, "/api/projects/"+projectID, nil), "id", projectID)).Want(http.StatusNoContent)
					}
				}
			}
			encoded, err := json.Marshal(resource)
			if err != nil {
				t.Fatal(err)
			}
			noteID := dbfx.Insert(t, "document_comment", testutil.Cols{"workspace_id": testWorkspaceID, "resource_key": resource.Kind + ":" + ownerID, "resource_ref": encoded, "content": "Source discussion", "author_id": testUserID})
			otherID := dbfx.Insert(t, "document_comment", testutil.Cols{"workspace_id": testWorkspaceID, "resource_key": "issue:" + uuid.NewString(), "resource_ref": `{"kind":"issue","id":"unrelated"}`, "content": "Another source discussion", "author_id": testUserID})
			remove()
			if count := dbfx.Count(t, "SELECT count(*) FROM document_comment WHERE id = $1", noteID); count != 0 {
				t.Fatalf("source deletion left %d discussion rows", count)
			}
			if count := dbfx.Count(t, "SELECT count(*) FROM document_comment WHERE id = $1", otherID); count != 1 {
				t.Fatal("source deletion removed an unrelated discussion")
			}
		})
	}
}

func TestDocumentCommentCannotReplyAcrossResources(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	first := dbfx.Issue(t, "First document discussion")
	second := dbfx.Issue(t, "Unrelated document discussion")
	dbfx.Cleanup(t, "DELETE FROM document_comment WHERE workspace_id = $1 AND resource_key = $2", testWorkspaceID, "issue:"+first)
	var root DocumentCommentResponse
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/api/document-comments", CreateDocumentCommentRequest{Resource: DocumentCommentResource{Kind: "issue", ID: first}, Content: "First document only"})).Want(http.StatusCreated).JSON(&root)

	resource := DocumentCommentResource{Kind: "issue", ID: second}
	testutil.Call(t, testHandler.CreateDocumentComment, newRequest(http.MethodPost, "/api/document-comments", CreateDocumentCommentRequest{Resource: resource, ParentID: &root.ID, Content: "Wrong document"})).Want(http.StatusNotFound)
	testutil.Call(t, testHandler.UpdateDocumentCommentResolution, withURLParam(newRequest(http.MethodPatch, "/api/document-comments/"+root.ID, ResolveDocumentCommentRequest{Resource: resource, Resolved: pBool(true), ExpectedRevision: 1}), "id", root.ID)).Want(http.StatusNotFound)

	var page struct {
		Comments []DocumentCommentResponse `json:"comments"`
	}
	testutil.Call(t, testHandler.QueryDocumentComments, newRequest(http.MethodPost, "/api/document-comments/query", QueryDocumentCommentsRequest{Resource: resource})).Want(http.StatusOK).JSON(&page)
	if len(page.Comments) != 0 {
		t.Fatalf("another document's discussion leaked: %#v", page.Comments)
	}
}
