package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestCreateIssue_persistsGoalInTheCreateTransaction(t *testing.T) {
	// Given
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	created := httptest.NewRecorder()
	testHandler.CreateIssue(created, newRequest(http.MethodPost, "/api/issues", map[string]any{
		"title":          "new goal-mode issue",
		"goal_objective": "Make the review gate visible at creation",
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("create goal-mode issue: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() { deleteTestIssue(t, response.ID) })
	issueID, err := util.ParseUUID(response.ID)
	if err != nil {
		t.Fatalf("parse created issue id: %v", err)
	}

	// When
	goal, err := testHandler.Queries.GetIssueGoal(t.Context(), issueID)

	// Then
	if err != nil {
		t.Fatalf("load created goal: %v", err)
	}
	if goal.Objective != "Make the review gate visible at creation" {
		t.Fatalf("goal objective = %q", goal.Objective)
	}
}

func TestIssueGoal_blocksReview_untilAssignedMemberCompletesGoal(t *testing.T) {
	// Given
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	issueID := createTestIssue(t, "goal-mode review gate", "todo", "none")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issueUUID, err := util.ParseUUID(issueID)
	if err != nil {
		t.Fatalf("parse issue id: %v", err)
	}
	userUUID, err := util.ParseUUID(testUserID)
	if err != nil {
		t.Fatalf("parse user id: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), "UPDATE issue SET assignee_type = 'member', assignee_id = $1 WHERE id = $2", userUUID, issueUUID); err != nil {
		t.Fatalf("assign goal owner: %v", err)
	}

	goal := httptest.NewRecorder()
	testHandler.UpsertIssueGoal(goal, withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID+"/goal", map[string]any{
		"objective": "Ship the review gate",
	}), "id", issueID))
	if goal.Code != http.StatusOK {
		t.Fatalf("enable goal mode: expected 200, got %d: %s", goal.Code, goal.Body.String())
	}

	// When
	blocked := httptest.NewRecorder()
	testHandler.UpdateIssue(blocked, withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{
		"status": "in_review",
	}), "id", issueID))

	// Then
	if blocked.Code != http.StatusConflict {
		t.Fatalf("review before goal completion: expected 409, got %d: %s", blocked.Code, blocked.Body.String())
	}

	// When
	completed := httptest.NewRecorder()
	testHandler.CompleteIssueGoal(completed, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/goal/complete", nil), "id", issueID))
	if completed.Code != http.StatusOK {
		t.Fatalf("complete goal: expected 200, got %d: %s", completed.Code, completed.Body.String())
	}
	review := httptest.NewRecorder()
	testHandler.UpdateIssue(review, withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{
		"status": "in_review",
	}), "id", issueID))

	// Then
	if review.Code != http.StatusOK {
		t.Fatalf("review after goal completion: expected 200, got %d: %s", review.Code, review.Body.String())
	}
}
