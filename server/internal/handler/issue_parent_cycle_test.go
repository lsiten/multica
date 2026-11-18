package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// A parent chain deeper than the old fixed validation limit must still reject
// assigning a root issue below one of its descendants.
func TestUpdateIssueRejectsDeepParentCycle(t *testing.T) {
	root := dbfx.Issue(t, "deep cycle root", testutil.Cols{"status": "todo"})
	parent := root
	for depth := 0; depth < 12; depth++ {
		parent = dbfx.Issue(t, "deep cycle parent", testutil.Cols{
			"status":          "todo",
			"parent_issue_id": parent,
		})
	}

	testutil.Call(t, testHandler.UpdateIssue, updateIssueRequest(root, map[string]any{
		"parent_issue_id": parent,
	})).Want(http.StatusBadRequest)
}
