package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// TestCreateGroupChatStandalone verifies a manually-created group chat is not a
// project default (is_project_default=false), carries no project (project_id
// null), includes the creator as a member participant, and — unlike a project's
// default group — can be archived manually.
func TestCreateGroupChatStandalone(t *testing.T) {
	agentID := createHandlerTestAgent(t, "StandaloneGroupAgent", []byte("[]"))

	body := map[string]any{
		"agent_id": agentID,
		"title":    "Standalone group",
		"participants": []map[string]any{
			{"member_type": "agent", "member_id": agentID},
		},
	}
	req := withChatTestWorkspaceCtx(t, newRequest("POST", "/api/chat/sessions/groups", body))

	var out ChatSessionResponse
	testutil.Call(t, testHandler.CreateGroupChat, req).Want(http.StatusCreated).JSON(&out)
	if out.ProjectID != nil {
		t.Fatalf("standalone group should have no project, got %v", out.ProjectID)
	}

	var kind, status string
	var isProjectDefault, projectNull bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT kind, is_project_default, status, project_id IS NULL FROM chat_session WHERE id = $1`,
		out.ID).Scan(&kind, &isProjectDefault, &status, &projectNull); err != nil {
		t.Fatalf("load session: %v", err)
	}
	if kind != "group" {
		t.Errorf("kind = %q, want group", kind)
	}
	if isProjectDefault {
		t.Errorf("is_project_default = %t, want false (standalone group is not a project default)", isProjectDefault)
	}
	if status != "active" {
		t.Errorf("status = %q, want active", status)
	}
	if !projectNull {
		t.Errorf("project_id should be null for a standalone group")
	}

	// Participants: the creator (member) plus the requested agent.
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat_participant WHERE chat_session_id = $1`, out.ID).Scan(&n); err != nil {
		t.Fatalf("count participants: %v", err)
	}
	if n != 2 {
		t.Errorf("participant count = %d, want 2 (creator + requested agent)", n)
	}
	var memberCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat_participant WHERE chat_session_id = $1 AND member_type = 'member'`,
		out.ID).Scan(&memberCount); err != nil {
		t.Fatalf("count member participants: %v", err)
	}
	if memberCount < 1 {
		t.Errorf("creator should be added as a member participant, got %d", memberCount)
	}

	// A standalone group is not a project default, so the manual-archive guard
	// must not flag it.
	loaded, err := testHandler.Queries.GetChatSession(context.Background(), parseUUID(out.ID))
	if err != nil {
		t.Fatalf("load session row: %v", err)
	}
	if isGroupChatManualArchive(loaded) {
		t.Errorf("standalone group must not be flagged as a manual archive target")
	}
}

// TestCreateGroupChatWithProject verifies a manually-created group may attach to
// a project while still being a standalone (non-default) group.
func TestCreateGroupChatWithProject(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ProjectBoundGroupAgent", []byte("[]"))
	projectID := createChatProjectTestProject(t, testWorkspaceID, "Group Project", "")

	body := map[string]any{
		"agent_id":   agentID,
		"title":      "Project group",
		"project_id": projectID,
	}
	req := withChatTestWorkspaceCtx(t, newRequest("POST", "/api/chat/sessions/groups", body))

	var out ChatSessionResponse
	testutil.Call(t, testHandler.CreateGroupChat, req).Want(http.StatusCreated).JSON(&out)

	var isProjectDefault, projectSet bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT is_project_default, project_id IS NOT NULL FROM chat_session WHERE id = $1`,
		out.ID).Scan(&isProjectDefault, &projectSet); err != nil {
		t.Fatalf("load session: %v", err)
	}
	if isProjectDefault {
		t.Errorf("is_project_default = %t, want false (a manual group is never a default even when bound to a project)", isProjectDefault)
	}
	if !projectSet {
		t.Errorf("project_id should be set for a project-bound group")
	}
}
