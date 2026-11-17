package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestIssueWorkflowExpectedUpdateRejectsStaleDraft(t *testing.T) {
	workflow := createTestWorkflow(t, "Conflict "+workflowTestSuffix(), "in_progress")
	first := withURLParam(workflowRequest(http.MethodPatch, "/api/issue-workflows/"+workflow.ID, map[string]any{
		"description": "Saved by another editor", "expected_updated_at": workflow.UpdatedAt,
	}), "id", workflow.ID)
	var saved IssueWorkflowResponse
	testutil.Call(t, testHandler.UpdateIssueWorkflow, first).Want(http.StatusOK).JSON(&saved)

	stale := withURLParam(workflowRequest(http.MethodPatch, "/api/issue-workflows/"+workflow.ID, map[string]any{
		"description": "Stale local draft", "expected_updated_at": workflow.UpdatedAt,
	}), "id", workflow.ID)
	testutil.Call(t, testHandler.UpdateIssueWorkflow, stale).Want(http.StatusConflict)

	current, err := testHandler.Queries.GetIssueWorkflow(context.Background(), db.GetIssueWorkflowParams{
		ID: parseUUID(workflow.ID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.Description != saved.Description {
		t.Fatalf("stale edit overwrote saved description: %q", current.Description)
	}
}

func TestIssueWorkflowExpectedUpdateRejectsMalformedTimestamp(t *testing.T) {
	workflow := createTestWorkflow(t, "Malformed version "+workflowTestSuffix(), "in_progress")
	request := withURLParam(workflowRequest(http.MethodPatch, "/api/issue-workflows/"+workflow.ID, map[string]any{
		"description": "Do not write", "expected_updated_at": "not-a-timestamp",
	}), "id", workflow.ID)
	testutil.Call(t, testHandler.UpdateIssueWorkflow, request).Want(http.StatusBadRequest)
}
