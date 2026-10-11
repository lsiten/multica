package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func withContentVersionURLParams(req *http.Request, id, revision string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	if revision != "" {
		rctx.URLParams.Add("revision", revision)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func issueDescription(ctx context.Context, t *testing.T, issueID string) string {
	t.Helper()
	var desc string
	if err := testPool.QueryRow(ctx, `SELECT description FROM issue WHERE id = $1`, issueID).Scan(&desc); err != nil {
		t.Fatalf("read issue description: %v", err)
	}
	return desc
}

func issueRevision(ctx context.Context, t *testing.T, issueID string) int64 {
	t.Helper()
	var rev int64
	if err := testPool.QueryRow(ctx, `SELECT revision FROM issue WHERE id = $1`, issueID).Scan(&rev); err != nil {
		t.Fatalf("read issue revision: %v", err)
	}
	return rev
}

func contentVersionCount(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM document_content_version WHERE owner_type = 'issue' AND owner_id = $1`,
		issueID).Scan(&n); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	return n
}

func pInt64(v int64) *int64 { return &v }

// TestDocumentContentVersionLifecycle is the end-to-end check for the
// content-version foundation: record -> apply to the issue body -> list -> get
// -> rollback, plus the compare-and-swap guard.
func TestDocumentContentVersionLifecycle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := dbfx.Issue(t, "doc-content-version-lifecycle-"+time.Now().Format("20060102150405.000000000"))
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	// The fixture leaves description NULL and revision at its default (1).
	var got DocumentContentVersionResponse
	body1 := "first version body"
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType:      "text/markdown",
			Content:          &body1,
			ExpectedRevision: pInt64(1),
		}), issueID, "")).
		Want(http.StatusCreated).JSON(&got)

	if got.Revision != 1 {
		t.Fatalf("expected first revision 1, got %d", got.Revision)
	}
	if got.Content == nil || *got.Content != body1 {
		t.Fatalf("expected content %q, got %#v", body1, got.Content)
	}
	// The edit must have landed on the issue body itself (D8: modification on
	// the document), and the issue revision must have advanced.
	if desc := issueDescription(ctx, t, issueID); desc != body1 {
		t.Fatalf("issue body not applied: got %q want %q", desc, body1)
	}
	revAfterV1 := issueRevision(ctx, t, issueID)

	// v2 supersedes v1.
	body2 := "second version body"
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType:      "text/markdown",
			Content:          &body2,
			ExpectedRevision: pInt64(revAfterV1),
		}), issueID, "")).
		Want(http.StatusCreated)

	// A stale expected_revision must conflict (409), not silently apply.
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType:      "text/markdown",
			Content:          &body2,
			ExpectedRevision: pInt64(revAfterV1 + 100),
		}), issueID, "")).
		Want(http.StatusConflict)

	// List returns newest first.
	var list []DocumentContentVersionResponse
	testutil.Call(t, testHandler.ListDocumentContentVersions,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/issues/"+issueID+"/content-versions", nil), issueID, "")).
		Want(http.StatusOK).JSON(&list)
	if len(list) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(list))
	}
	if list[0].Revision != 2 || list[1].Revision != 1 {
		t.Fatalf("expected newest-first [2,1], got [%d,%d]", list[0].Revision, list[1].Revision)
	}

	// Fetch v1 by revision.
	var single DocumentContentVersionResponse
	testutil.Call(t, testHandler.GetDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/issues/"+issueID+"/content-versions/1", nil), issueID, "1")).
		Want(http.StatusOK).JSON(&single)
	if single.Revision != 1 || single.Content == nil || *single.Content != body1 {
		t.Fatalf("unexpected v1: %#v", single)
	}

	// A missing revision 404s.
	testutil.Call(t, testHandler.GetDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/issues/"+issueID+"/content-versions/999", nil), issueID, "999")).
		Want(http.StatusNotFound)

	// Rollback to v1 restores v1's content and records it as v3 (history stays
	// linear; it is not a delete of v2).
	var rolledBack DocumentContentVersionResponse
	testutil.Call(t, testHandler.RollbackDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions/1/rollback", nil), issueID, "1")).
		Want(http.StatusCreated).JSON(&rolledBack)
	if rolledBack.Revision != 3 {
		t.Fatalf("expected rollback to be recorded as revision 3, got %d", rolledBack.Revision)
	}
	if rolledBack.Content == nil || *rolledBack.Content != body1 {
		t.Fatalf("expected rollback content %q, got %#v", body1, rolledBack.Content)
	}
	if desc := issueDescription(ctx, t, issueID); desc != body1 {
		t.Fatalf("rollback did not restore the issue body: got %q want %q", desc, body1)
	}
	if n := contentVersionCount(t, issueID); n != 3 {
		t.Fatalf("expected 3 versions after rollback, got %d", n)
	}
}

// TestDocumentContentVersionRejectsBinaryOnIssueBody guards the boundary that a
// binary version cannot be committed onto an issue's Markdown body.
func TestDocumentContentVersionRejectsBinaryOnIssueBody(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "doc-content-version-binary-"+time.Now().Format("20060102150405.000000000"))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	url := "https://blob.example/test.pdf"
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "application/pdf",
			ContentURL:  &url,
		}), issueID, "")).
		Want(http.StatusNotImplemented)
}

var _ = db.DocumentContentVersion{}

// TestDocumentContentVersionMerge checks the D4 merge rule: a consecutive edit
// by the same actor with Merge=true folds into that actor's latest version
// instead of appending a new revision.
func TestDocumentContentVersionMerge(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := dbfx.Issue(t, "doc-content-version-merge-"+time.Now().Format("20060102150405.000000000"))
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	body1 := "merged: first"
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "text/markdown",
			Content:     &body1,
		}), issueID, "")).
		Want(http.StatusCreated)

	// The same actor's consecutive edit, marked Merge=true, must fold into
	// revision 1 (no new revision is created) and update its content.
	mergedBody := "merged: second"
	var merged DocumentContentVersionResponse
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "text/markdown",
			Content:     &mergedBody,
			Merge:       pBool(true),
		}), issueID, "")).
		Want(http.StatusOK).JSON(&merged)

	if merged.Revision != 1 {
		t.Fatalf("expected merge to keep revision 1, got %d", merged.Revision)
	}
	if !merged.Merged {
		t.Fatalf("expected merged=true on the response")
	}
	if merged.Content == nil || *merged.Content != mergedBody {
		t.Fatalf("expected merged content %q, got %#v", mergedBody, merged.Content)
	}
	// No new revision was created: still exactly one version row.
	if n := contentVersionCount(t, issueID); n != 1 {
		t.Fatalf("expected 1 version after merge, got %d", n)
	}
	// The body itself advanced to the merged content.
	if desc := issueDescription(ctx, t, issueID); desc != mergedBody {
		t.Fatalf("issue body not updated to merged content: got %q want %q", desc, mergedBody)
	}
}

// TestDocumentContentVersionAtomicity checks that a failed CAS leaves the issue
// body and the version log consistent (no half-applied state).
func TestDocumentContentVersionAtomicity(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := dbfx.Issue(t, "doc-content-version-atomic-"+time.Now().Format("20060102150405.000000000"))
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	body := "atomic body"
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "text/markdown",
			Content:     &body,
		}), issueID, "")).
		Want(http.StatusCreated)

	// A stale CAS must fail without leaving any new version row.
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType:      "text/markdown",
			Content:          &body,
			ExpectedRevision: pInt64(999),
		}), issueID, "")).
		Want(http.StatusConflict)

	if n := contentVersionCount(t, issueID); n != 1 {
		t.Fatalf("expected exactly 1 version (no half-applied write), got %d", n)
	}
}

func pBool(v bool) *bool { return &v }

// TestIssueDescriptionUpdateRecordsContentVersion verifies the write-side
// integration: a Markdown body edit through the ordinary issue-update path
// records a content version (so the diff/rollback history is populated by
// edits, not only the dedicated content-version endpoint), and a distinct
// later edit appends a further version.
func TestIssueDescriptionUpdateRecordsContentVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := dbfx.Issue(t, "doc-content-version-update-"+time.Now().Format("20060102150405.000000000"))
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	rec := httptest.NewRecorder()
	testHandler.UpdateIssue(rec, withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{
		"description": "edited via issue update",
	}), "id", issueID))
	if rec.Code != http.StatusOK {
		t.Fatalf("update issue = %d: %s", rec.Code, rec.Body.String())
	}
	if desc := issueDescription(ctx, t, issueID); desc != "edited via issue update" {
		t.Fatalf("issue description = %q, want the edit", desc)
	}
	if n := contentVersionCount(t, issueID); n != 1 {
		t.Fatalf("expected exactly 1 content version after a description update, got %d", n)
	}

	// A second description edit appends a further version (merge is off for the
	// issue-update path, so each real edit is its own version).
	rec2 := httptest.NewRecorder()
	testHandler.UpdateIssue(rec2, withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{
		"description": "edited again",
	}), "id", issueID))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second update issue = %d: %s", rec2.Code, rec2.Body.String())
	}
	if n := contentVersionCount(t, issueID); n != 2 {
		t.Fatalf("expected 2 content versions after two distinct description updates, got %d", n)
	}
}


// attachmentContentVersionCount counts the version rows for one attachment.
func attachmentContentVersionCount(t *testing.T, attachmentID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM document_content_version WHERE owner_type = 'attachment' AND owner_id = $1`,
		attachmentID).Scan(&n); err != nil {
		t.Fatalf("count attachment versions: %v", err)
	}
	return n
}

// createAttachmentForContentVersionTest inserts a binary attachment into the
// test workspace so the attachment content-version endpoints have a real owner
// to resolve (loadAttachmentForRequest reads the workspace from the request
// header, which newRequest pins to testWorkspaceID).
func createAttachmentForContentVersionTest(t *testing.T, filename, contentType, url string, sizeBytes int64) string {
	t.Helper()
	workspaceUUID, _ := util.ParseUUID(testWorkspaceID)
	userUUID, _ := util.ParseUUID(testUserID)
	attID := uuid.New()
	_, err := testHandler.Queries.CreateAttachment(context.Background(), db.CreateAttachmentParams{
		ID:           pgtype.UUID{Bytes: attID, Valid: true},
		WorkspaceID:  workspaceUUID,
		UploaderType: "member",
		UploaderID:   userUUID,
		Filename:     filename,
		Url:          url,
		ContentType:  contentType,
		SizeBytes:    sizeBytes,
	})
	if err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1`, attID)
	})
	return attID.String()
}

// TestAttachmentDocumentContentVersionLifecycle is the end-to-end check for the
// binary (attachment) content-version path: record -> list -> get -> rollback.
// A binary is never rewritten in place, so each edit appends a new version row
// whose content_url points at a new blob; rollback appends another.
func TestAttachmentDocumentContentVersionLifecycle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	attID := createAttachmentForContentVersionTest(t, "chart.png", "image/png", "https://blob.example/orig.png", 100)

	// Record v1 (a new blob).
	var got DocumentContentVersionResponse
	url1 := "https://blob.example/v1.png"
	testutil.Call(t, testHandler.RecordDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/attachments/"+attID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "image/png",
			ContentURL:  &url1,
			SizeBytes:   200,
		}), attID, "")).
		Want(http.StatusCreated).JSON(&got)
	if got.Revision != 1 {
		t.Fatalf("expected first revision 1, got %d", got.Revision)
	}
	if got.ContentURL == nil || *got.ContentURL != url1 {
		t.Fatalf("expected content_url %q, got %#v", url1, got.ContentURL)
	}
	if got.SizeBytes != 200 {
		t.Fatalf("expected size 200, got %d", got.SizeBytes)
	}

	// Record v2.
	url2 := "https://blob.example/v2.png"
	testutil.Call(t, testHandler.RecordDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/attachments/"+attID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "image/png",
			ContentURL:  &url2,
			SizeBytes:   300,
		}), attID, "")).
		Want(http.StatusCreated)

	// List returns newest first.
	var list []DocumentContentVersionResponse
	testutil.Call(t, testHandler.ListDocumentContentVersionsForAttachment,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/attachments/"+attID+"/content-versions", nil), attID, "")).
		Want(http.StatusOK).JSON(&list)
	if len(list) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(list))
	}
	if list[0].Revision != 2 || list[1].Revision != 1 {
		t.Fatalf("expected newest-first [2,1], got [%d,%d]", list[0].Revision, list[1].Revision)
	}

	// Fetch v1 by revision.
	var single DocumentContentVersionResponse
	testutil.Call(t, testHandler.GetDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/attachments/"+attID+"/content-versions/1", nil), attID, "1")).
		Want(http.StatusOK).JSON(&single)
	if single.Revision != 1 || single.ContentURL == nil || *single.ContentURL != url1 {
		t.Fatalf("unexpected v1: %#v", single)
	}

	// A missing revision 404s.
	testutil.Call(t, testHandler.GetDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/attachments/"+attID+"/content-versions/999", nil), attID, "999")).
		Want(http.StatusNotFound)

	// Rollback to v1 records v3 pointing back at v1's blob (history stays linear).
	var rolledBack DocumentContentVersionResponse
	testutil.Call(t, testHandler.RollbackDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/attachments/"+attID+"/content-versions/1/rollback", nil), attID, "1")).
		Want(http.StatusCreated).JSON(&rolledBack)
	if rolledBack.Revision != 3 {
		t.Fatalf("expected rollback recorded as revision 3, got %d", rolledBack.Revision)
	}
	if rolledBack.ContentURL == nil || *rolledBack.ContentURL != url1 {
		t.Fatalf("expected rollback blob %q, got %#v", url1, rolledBack.ContentURL)
	}
	if n := attachmentContentVersionCount(t, attID); n != 3 {
		t.Fatalf("expected 3 versions after rollback, got %d", n)
	}
}

// TestAttachmentDocumentContentVersionMerge checks the D4 merge rule on the
// binary path: a consecutive same-actor edit with Merge=true folds into the
// latest version instead of appending a new revision.
func TestAttachmentDocumentContentVersionMerge(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	attID := createAttachmentForContentVersionTest(t, "chart.png", "image/png", "https://blob.example/orig.png", 100)

	url1 := "https://blob.example/v1.png"
	testutil.Call(t, testHandler.RecordDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/attachments/"+attID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "image/png",
			ContentURL:  &url1,
			SizeBytes:   200,
		}), attID, "")).
		Want(http.StatusCreated)

	url2 := "https://blob.example/v2.png"
	var merged DocumentContentVersionResponse
	testutil.Call(t, testHandler.RecordDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/attachments/"+attID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "image/png",
			ContentURL:  &url2,
			SizeBytes:   300,
			Merge:       pBool(true),
		}), attID, "")).
		Want(http.StatusOK).JSON(&merged)
	if merged.Revision != 1 {
		t.Fatalf("expected merge to keep revision 1, got %d", merged.Revision)
	}
	if !merged.Merged {
		t.Fatalf("expected merged=true on the response")
	}
	if merged.ContentURL == nil || *merged.ContentURL != url2 {
		t.Fatalf("expected merged blob %q, got %#v", url2, merged.ContentURL)
	}
	if n := attachmentContentVersionCount(t, attID); n != 1 {
		t.Fatalf("expected 1 version after merge, got %d", n)
	}
}

// TestAttachmentDocumentContentVersionRejectsText guards the boundary that an
// attachment (binary) version cannot be committed with inline text: exactly one
// of content / content_url must be set, and a binary needs content_url.
func TestAttachmentDocumentContentVersionRejectsText(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	attID := createAttachmentForContentVersionTest(t, "chart.png", "image/png", "https://blob.example/orig.png", 100)

	body := "some inline text"
	testutil.Call(t, testHandler.RecordDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/attachments/"+attID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "text/markdown",
			Content:     &body,
		}), attID, "")).
		Want(http.StatusBadRequest)
}


// TestDocumentContentVersionLatest checks the read-path endpoint returns the
// newest version (the current content an agent reads) and 404s before any
// history exists.
func TestDocumentContentVersionLatest(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "doc-content-version-latest-"+time.Now().Format("20060102150405.000000000"))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	// No version history yet: latest 404s.
	testutil.Call(t, testHandler.GetDocumentContentVersionLatest,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/issues/"+issueID+"/content-versions/current", nil), issueID, "")).
		Want(http.StatusNotFound)

	body1 := "latest: first"
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "text/markdown", Content: &body1,
		}), issueID, "")).
		Want(http.StatusCreated)
	body2 := "latest: second"
	testutil.Call(t, testHandler.RecordDocumentContentVersion,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/issues/"+issueID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "text/markdown", Content: &body2,
		}), issueID, "")).
		Want(http.StatusCreated)

	var latest DocumentContentVersionResponse
	testutil.Call(t, testHandler.GetDocumentContentVersionLatest,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/issues/"+issueID+"/content-versions/current", nil), issueID, "")).
		Want(http.StatusOK).JSON(&latest)
	if latest.Revision != 2 {
		t.Fatalf("expected latest revision 2, got %d", latest.Revision)
	}
	if latest.Content == nil || *latest.Content != body2 {
		t.Fatalf("expected latest content %q, got %#v", body2, latest.Content)
	}
}

// TestAttachmentDocumentContentVersionLatest checks the binary read-path returns
// the newest version (current blob) and 404s before any history exists.
func TestAttachmentDocumentContentVersionLatest(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	attID := createAttachmentForContentVersionTest(t, "chart.png", "image/png", "https://blob.example/orig.png", 100)

	testutil.Call(t, testHandler.GetDocumentContentVersionLatestForAttachment,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/attachments/"+attID+"/content-versions/current", nil), attID, "")).
		Want(http.StatusNotFound)

	url1 := "https://blob.example/v1.png"
	testutil.Call(t, testHandler.RecordDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/attachments/"+attID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "image/png", ContentURL: &url1, SizeBytes: 200,
		}), attID, "")).
		Want(http.StatusCreated)
	url2 := "https://blob.example/v2.png"
	testutil.Call(t, testHandler.RecordDocumentContentVersionForAttachment,
		withContentVersionURLParams(newRequest(http.MethodPost, "/api/attachments/"+attID+"/content-versions", RecordDocumentContentVersionRequest{
			ContentType: "image/png", ContentURL: &url2, SizeBytes: 300,
		}), attID, "")).
		Want(http.StatusCreated)

	var latest DocumentContentVersionResponse
	testutil.Call(t, testHandler.GetDocumentContentVersionLatestForAttachment,
		withContentVersionURLParams(newRequest(http.MethodGet, "/api/attachments/"+attID+"/content-versions/current", nil), attID, "")).
		Want(http.StatusOK).JSON(&latest)
	if latest.Revision != 2 {
		t.Fatalf("expected latest revision 2, got %d", latest.Revision)
	}
	if latest.ContentURL == nil || *latest.ContentURL != url2 {
		t.Fatalf("expected latest blob %q, got %#v", url2, latest.ContentURL)
	}
}
