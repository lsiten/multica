package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
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
		IssueID:           parseUUID(issueID),
		LifecycleStatus:   "pending_confirm",
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

func TestRequirementSubIssueCreationRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	requirementID := dbfx.Issue(t, "gated requirement", testutil.Cols{"kind": "requirement", "status": "todo"})
	if _, err := testHandler.Queries.CreateRequirement(ctx, db.CreateRequirementParams{
		WorkspaceID: parseUUID(testWorkspaceID), IssueID: parseUUID(requirementID),
		LifecycleStatus: "pending_confirm", UnconfirmedChange: false,
	}); err != nil {
		t.Fatalf("create requirement: %v", err)
	}
	create := func(title string) error {
		_, err := testHandler.IssueService.Create(ctx, service.IssueCreateParams{
			WorkspaceID: parseUUID(testWorkspaceID), Title: title, Status: "todo", Priority: "none",
			CreatorType: "member", CreatorID: parseUUID(testUserID), ParentIssueID: parseUUID(requirementID),
		}, service.IssueCreateOpts{})
		return err
	}
	if err := create("blocked before confirmation"); !errors.Is(err, service.ErrRequirementNotConfirmed) {
		t.Fatalf("pending requirement create error = %v, want ErrRequirementNotConfirmed", err)
	}
	nestedParent := dbfx.Issue(t, "existing nested parent", testutil.Cols{"parent_issue_id": requirementID, "status": "todo"})
	_, nestedErr := testHandler.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID: parseUUID(testWorkspaceID), Title: "nested bypass", Status: "todo", Priority: "none",
		CreatorType: "member", CreatorID: parseUUID(testUserID), ParentIssueID: parseUUID(nestedParent),
	}, service.IssueCreateOpts{})
	if !errors.Is(nestedErr, service.ErrRequirementNotConfirmed) {
		t.Fatalf("nested pending requirement create error = %v, want ErrRequirementNotConfirmed", nestedErr)
	}
	movable := dbfx.Issue(t, "move under pending requirement", testutil.Cols{"status": "todo"})
	moveReq := withURLParam(newRequest("PATCH", "/api/issues/"+movable, map[string]any{"parent_issue_id": requirementID}), "id", movable)
	testutil.Call(t, testHandler.UpdateIssue, moveReq).Want(http.StatusConflict)
	if _, err := testHandler.confirmRequirement(ctx, parseUUID(requirementID), parseUUID(testWorkspaceID), parseUUID(testUserID), 1); err != nil {
		t.Fatalf("confirm requirement: %v", err)
	}
	if err := create("allowed after confirmation"); err != nil {
		t.Fatalf("confirmed requirement create: %v", err)
	}
	requirement, err := testHandler.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: parseUUID(requirementID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatalf("load requirement: %v", err)
	}
	testHandler.maybeReopenRequirementOnEdit(ctx, requirement)
	if err := create("blocked after edit"); !errors.Is(err, service.ErrRequirementNotConfirmed) {
		t.Fatalf("changed requirement create error = %v, want ErrRequirementNotConfirmed", err)
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

func TestRequirementChangeReviewScopesAffectedSubIssues(t *testing.T) {
	requirementID := dbfx.Issue(t, "scoped requirement", testutil.Cols{"kind": "requirement", "status": "todo"})
	affectedID := dbfx.Issue(t, "affected delivery", testutil.Cols{"parent_issue_id": requirementID, "status": "in_progress"})
	dbfx.Issue(t, "unaffected delivery", testutil.Cols{"parent_issue_id": requirementID, "status": "todo"})

	decisions, ok := testHandler.changeReviewDecisionsForSubIssues(
		context.Background(),
		parseUUID(testWorkspaceID),
		parseUUID(requirementID),
		[]string{affectedID},
		ChangeImpactRedo,
	)
	if !ok || len(decisions) != 1 || decisions[0].SubIssueID != parseUUID(affectedID) {
		t.Fatalf("scoped decisions = %+v, ok=%v; want only %s", decisions, ok, affectedID)
	}
	unknownID := dbfx.Issue(t, "unrelated issue", testutil.Cols{"status": "todo"})
	if _, ok := testHandler.changeReviewDecisionsForSubIssues(
		context.Background(),
		parseUUID(testWorkspaceID),
		parseUUID(requirementID),
		[]string{unknownID},
		ChangeImpactRedo,
	); ok {
		t.Fatal("unknown affected issue was accepted")
	}
}
