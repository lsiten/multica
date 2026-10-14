package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestRequirementLifecycle exercises the requirement dimension end to end: a
// kind=requirement issue carries a 1:1 requirement row (created with the issue),
// which starts pending_confirm, is confirmed by pinning the document revision,
// and re-opens when the document is edited after confirmation (the edit IS the
// change). This is the flow the agent's confirm/change cards drive.
func TestRequirementLifecycle(t *testing.T) {
	issueID := dbfx.Issue(t, "requirement document", testutil.Cols{
		"kind":   "requirement",
		"status": "todo",
	})
	dbfx.Cleanup(t, "DELETE FROM requirement WHERE issue_id=$1", issueID)
	if _, err := testHandler.Queries.CreateRequirement(context.Background(), db.CreateRequirementParams{
		WorkspaceID:       parseUUID(testWorkspaceID),
		IssueID:         parseUUID(issueID),
		LifecycleStatus: "pending_confirm",
		UnconfirmedChange: false,
	}); err != nil {
		t.Fatalf("create requirement: %v", err)
	}

	lifecycle := func() RequirementResponse {
		req := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/requirement", nil), "id", issueID)
		var out RequirementResponse
		testutil.Call(t, testHandler.GetRequirement, req).Want(http.StatusOK).JSON(&out)
		return out
	}

	if r := lifecycle(); r.LifecycleStatus != "pending_confirm" {
		t.Fatalf("initial lifecycle = %q, want pending_confirm", r.LifecycleStatus)
	}

	if _, err := testHandler.confirmRequirement(context.Background(), parseUUID(issueID), parseUUID(testWorkspaceID), parseUUID(testUserID), 3); err != nil {
		t.Fatalf("confirm requirement: %v", err)
	}
	if r := lifecycle(); r.LifecycleStatus != "confirmed" || r.ConfirmedRevision != 3 {
		t.Fatalf("after confirm = %+v, want confirmed@3", r)
	}

	issue, err := testHandler.Queries.GetIssueInWorkspace(context.Background(), db.GetIssueInWorkspaceParams{
		ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	testHandler.maybeReopenRequirementOnEdit(context.Background(), issue)
	if r := lifecycle(); r.LifecycleStatus != "pending_confirm" || !r.UnconfirmedChange {
		t.Fatalf("after doc edit = %+v, want pending_confirm + unconfirmed_change", r)
	}
}

// TestGetRequirement404ForTask verifies the requirement endpoint only answers for
// a kind=requirement issue; a task issue has no requirement row.
func TestGetRequirement404ForTask(t *testing.T) {
	issueID := dbfx.Issue(t, "ordinary task", testutil.Cols{
		"kind":   "task",
		"status": "todo",
	})
	req := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/requirement", nil), "id", issueID)
	testutil.Call(t, testHandler.GetRequirement, req).Want(http.StatusNotFound)
}
