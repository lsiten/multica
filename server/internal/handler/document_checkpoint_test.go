package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestDocumentNamedCheckpointSurvivesAutosave(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, kind := range []string{"issue", "attachment"} {
		t.Run(kind, func(t *testing.T) {
			var id, path string
			var record, get func(http.ResponseWriter, *http.Request)
			initial := "Approved document body"
			changed := "Later automatic edits"
			name := "Approved checkpoint"
			first := RecordDocumentContentVersionRequest{ContentType: "text/markdown", Content: &initial, Note: &name}
			next := RecordDocumentContentVersionRequest{ContentType: "text/markdown", Content: &changed, Merge: pBool(true)}
			switch kind {
			case "issue":
				id = dbfx.Issue(t, "Named checkpoint document")
				path = "/api/issues/" + id + "/content-versions"
				record, get = testHandler.RecordDocumentContentVersion, testHandler.GetDocumentContentVersion
			case "attachment":
				id = dbfx.Insert(t, "attachment", testutil.Cols{
					"workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": testUserID,
					"filename": "chart.png", "url": "https://blob.example/original.png", "content_type": "image/png", "size_bytes": 100,
				})
				path = "/api/attachments/" + id + "/content-versions"
				record, get = testHandler.RecordDocumentContentVersionForAttachment, testHandler.GetDocumentContentVersionForAttachment
				initial, changed = "https://blob.example/checkpoint.png", "https://blob.example/autosave.png"
				first.ContentType, first.Content, first.ContentURL = "image/png", nil, &initial
				next.ContentType, next.Content, next.ContentURL = "image/png", nil, &changed
			default:
				t.Fatalf("unsupported fixture kind %q", kind)
			}
			testutil.Call(t, record, withContentVersionURLParams(newRequest(http.MethodPost, path, first), id, "")).Want(http.StatusCreated)

			testutil.Call(t, record, withContentVersionURLParams(newRequest(http.MethodPost, path, next), id, "")).Want(http.StatusCreated)

			var checkpoint DocumentContentVersionResponse
			testutil.Call(t, get, withContentVersionURLParams(newRequest(http.MethodGet, path+"/1", nil), id, "1")).Want(http.StatusOK).JSON(&checkpoint)
			if checkpoint.Note == nil || *checkpoint.Note != name || checkpoint.Merged {
				t.Fatalf("named checkpoint metadata changed: %#v", checkpoint)
			}
			var original *string
			switch kind {
			case "issue":
				original = checkpoint.Content
			case "attachment":
				original = checkpoint.ContentURL
			}
			if original == nil || *original != initial {
				t.Fatalf("checkpoint content changed: got %v, want %q", original, initial)
			}
		})
	}
}

func TestDocumentFirstEditPreservesOriginalBody(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	original := "Existing document before its first editor save"
	issueID := dbfx.Issue(t, "First document edit", testutil.Cols{"description": original})
	path := "/api/issues/" + issueID + "/content-versions"
	changed := "The first automatic save"

	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, path, RecordDocumentContentVersionRequest{
			ContentType: "text/markdown", Content: &changed, Merge: pBool(true), ExpectedRevision: pInt64(1),
		}), issueID, "")).Want(http.StatusCreated)

	var baseline DocumentContentVersionResponse
	testutil.Call(t, testHandler.GetDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodGet, path+"/1", nil), issueID, "1")).Want(http.StatusOK).JSON(&baseline)
	if baseline.Content == nil || *baseline.Content != original {
		t.Fatalf("first edit lost the original body: got %v, want %q", baseline.Content, original)
	}
	var current DocumentContentVersionResponse
	testutil.Call(t, testHandler.GetDocumentContentVersionLatest,
		withContentVersionURLParams(newRequest(http.MethodGet, path+"/current", nil), issueID, "")).Want(http.StatusOK).JSON(&current)
	if current.Content == nil || *current.Content != changed || current.Revision != 2 {
		t.Fatalf("first edit was not recorded after its baseline: %#v", current)
	}
}

func TestDocumentOrdinaryIssueEditPreservesOriginalBody(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	original := "Original issue document before editing"
	issueID := dbfx.Issue(t, "Ordinary document edit", testutil.Cols{"description": original})
	testutil.Call(t, testHandler.UpdateIssue,
		withURLParam(newRequest(http.MethodPatch, "/api/issues/"+issueID, map[string]any{"description": "Updated through the issue editor"}), "id", issueID)).Want(http.StatusOK)
	path := "/api/issues/" + issueID + "/content-versions"
	var baseline DocumentContentVersionResponse
	testutil.Call(t, testHandler.GetDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodGet, path+"/1", nil), issueID, "1")).Want(http.StatusOK).JSON(&baseline)
	if baseline.Content == nil || *baseline.Content != original {
		t.Fatalf("ordinary issue edit lost its original body: %#v", baseline)
	}
	var latest DocumentContentVersionResponse
	testutil.Call(t, testHandler.GetDocumentContentVersionLatest,
		withContentVersionURLParams(newRequest(http.MethodGet, path+"/current", nil), issueID, "")).Want(http.StatusOK).JSON(&latest)
	if latest.Revision != 2 || latest.Content == nil || *latest.Content != "Updated through the issue editor" {
		t.Fatalf("ordinary issue edit did not append after its baseline: %#v", latest)
	}
}
