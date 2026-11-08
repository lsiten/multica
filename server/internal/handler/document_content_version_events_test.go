package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestDocumentCurrentOwnerRevisionAfterMetadataEdit(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database is required for document revision verification")
	}
	for _, metadataChange := range []bool{false, true} {
		name := "first_edit"
		if metadataChange {
			name = "after_metadata_edit"
		}
		t.Run(name, func(t *testing.T) {
			issueID := dbfx.Issue(t, "document owner revision")
			body := "original body"
			testutil.Call(t, testHandler.RecordDocumentContentVersion,
				withContentVersionURLParams(newRequest(http.MethodPost, "/", RecordDocumentContentVersionRequest{
					ContentType: "text/markdown", Content: &body, ExpectedRevision: pInt64(1),
				}), issueID, "")).Want(http.StatusCreated)
			if metadataChange {
				if _, err := testPool.Exec(context.Background(), "UPDATE issue SET title='renamed', revision=revision+1 WHERE id=$1", issueID); err != nil {
					t.Fatal(err)
				}
			}
			wantOwnerRevision := int64(2)
			if metadataChange {
				wantOwnerRevision = 3
			}
			var current struct {
				Revision      int64   `json:"revision"`
				OwnerRevision *int64  `json:"owner_revision"`
				Content       *string `json:"content"`
			}
			testutil.Call(t, testHandler.GetDocumentContentVersionLatest,
				withContentVersionURLParams(newRequest(http.MethodGet, "/", nil), issueID, "")).Want(http.StatusOK).JSON(&current)
			if current.Revision != 1 || current.OwnerRevision == nil || *current.OwnerRevision != wantOwnerRevision {
				t.Fatalf("current document must distinguish history revision 1 from owner revision %d: %+v", wantOwnerRevision, current)
			}
			if current.Content == nil || *current.Content != body {
				t.Fatalf("current body = %v, want %q", current.Content, body)
			}
			changed := "saved after metadata change"
			testutil.Call(t, testHandler.RecordDocumentContentVersion,
				withContentVersionURLParams(newRequest(http.MethodPost, "/", RecordDocumentContentVersionRequest{
					ContentType: "text/markdown", Content: &changed, ExpectedRevision: current.OwnerRevision,
				}), issueID, "")).Want(http.StatusCreated)
		})
	}
}

func TestDocumentContentVersionPublishesCommittedMetadata(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database is required for document notification verification")
	}
	for _, kind := range []string{"issue", "attachment"} {
		t.Run(kind, func(t *testing.T) {
			h := *testHandler
			h.Bus = events.New()
			ownerID := ""
			body := "body excluded from event"
			blobURL := "https://blob.example/private-object"
			input := RecordDocumentContentVersionRequest{ContentType: "text/markdown", Content: &body}
			record := h.RecordDocumentContentVersion
			rollback := h.RollbackDocumentContentVersion
			if kind == "issue" {
				ownerID = dbfx.Issue(t, "document event")
			} else {
				ownerID = createAttachmentForContentVersionTest(t, "event.png", "image/png", blobURL, 12)
				input = RecordDocumentContentVersionRequest{ContentType: "image/png", ContentURL: &blobURL}
				record = h.RecordDocumentContentVersionForAttachment
				rollback = h.RollbackDocumentContentVersionForAttachment
			}
			var received []events.Event
			var committedCounts []int
			var commitReadError error
			h.Bus.Subscribe("document_content:changed", func(event events.Event) {
				received = append(received, event)
				var count int
				if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM document_content_version WHERE owner_type=$1 AND owner_id=$2 AND workspace_id=$3", kind, ownerID, testWorkspaceID).Scan(&count); err != nil {
					commitReadError = err
				}
				committedCounts = append(committedCounts, count)
			})
			testutil.Call(t, record, withContentVersionURLParams(newRequest(http.MethodPost, "/", input), ownerID, "")).Want(http.StatusCreated)
			merge := true
			input.Merge = &merge
			testutil.Call(t, record, withContentVersionURLParams(newRequest(http.MethodPost, "/", input), ownerID, "")).Want(http.StatusOK)
			testutil.Call(t, rollback, withContentVersionURLParams(newRequest(http.MethodPost, "/", nil), ownerID, "1")).Want(http.StatusCreated)
			if commitReadError != nil {
				t.Fatal(commitReadError)
			}
			if len(received) != 3 {
				t.Fatalf("append, merge and rollback must each publish after commit; got %d events", len(received))
			}
			for index, event := range received {
				encoded, err := json.Marshal(event.Payload)
				if err != nil {
					t.Fatal(err)
				}
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(encoded, &payload); err != nil {
					t.Fatal(err)
				}
				if event.WorkspaceID != testWorkspaceID || len(payload) != 3 {
					t.Fatalf("event must contain only scoped owner metadata: %+v %s", event, encoded)
				}
				var ownerType, id string
				var revision int64
				if err := json.Unmarshal(payload["owner_type"], &ownerType); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(payload["owner_id"], &id); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(payload["revision"], &revision); err != nil {
					t.Fatal(err)
				}
				wantRevision := int64(1)
				if index == 2 {
					wantRevision = 2
				}
				if committedCounts[index] != int(wantRevision) {
					t.Fatalf("event %d arrived before the version committed: count=%d", index, committedCounts[index])
				}
				if ownerType != kind || id != ownerID || revision != wantRevision {
					t.Fatalf("wrong document event: %s", encoded)
				}
			}
			input.ContentType = ""
			testutil.Call(t, record, withContentVersionURLParams(newRequest(http.MethodPost, "/", input), ownerID, "")).Want(http.StatusBadRequest)
			if len(received) != 3 {
				t.Fatal("failed writes must not publish")
			}
		})
	}
}
