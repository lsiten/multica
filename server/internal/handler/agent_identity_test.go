package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestNormalizeAgentIdentity(t *testing.T) {
	params, err := normalizeAgentIdentity(updateAgentIdentityRequest{
		Email: " Cue@example.com ", Phone: "+8613800138000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if params.Email.String != "cue@example.com" || !params.Email.Valid || params.BudgetUsdTicks != 0 || params.WalletAddress.Valid {
		t.Fatalf("unexpected normalized identity: %+v", params)
	}
}

func TestNormalizeAgentIdentityNormalizesMainlandPhone(t *testing.T) {
	params, err := normalizeAgentIdentity(updateAgentIdentityRequest{Phone: "18565884671"})
	if err != nil {
		t.Fatal(err)
	}
	if params.Phone.String != "+8618565884671" {
		t.Fatalf("phone=%q, want +8618565884671", params.Phone.String)
	}
}

func identityRequest(t *testing.T, method, agentID string, body any) *http.Request {
	t.Helper()
	return testutil.WithURLParams(testutil.WithHeaders(
		testutil.JSONRequest(method, "/api/agents/"+agentID+"/identity", body),
		"X-User-ID", testUserID, "X-Workspace-ID", testWorkspaceID,
	), "id", agentID)
}

func TestAgentIdentityRejectsMachineCredentials(t *testing.T) {
	id := dbfx.Agent(t, "Identity protected", "")
	for _, source := range []string{"task_token", "cloud_pat"} {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			t.Run(source+method, func(t *testing.T) {
				req := identityRequest(t, method, id, `{}`)
				req.Header.Set("X-Actor-Source", source)
				handler := testHandler.GetAgentIdentity
				if method == http.MethodPut {
					handler = testHandler.UpdateAgentIdentity
				}
				testutil.Call(t, handler, req).Want(http.StatusForbidden)
			})
		}
	}
}

func TestAgentIdentityRejectsNullAndInvalidBodies(t *testing.T) {
	id := dbfx.Agent(t, "Identity validation", "")
	for _, raw := range []string{`null`, `[]`, `{} {}`, `{"email":"` + strings.Repeat("a", 321) + `@example.test"}`} {
		t.Run(raw[:min(len(raw), 20)], func(t *testing.T) {
			testutil.Call(t, testHandler.UpdateAgentIdentity, identityRequest(t, http.MethodPut, id, raw)).Want(http.StatusBadRequest)
		})
	}
}

func TestAgentIdentityRoundTripAndAudit(t *testing.T) {
	id := dbfx.Agent(t, "Identity round trip", "")
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM agent_identity WHERE agent_id=$1`, id) })
	input := updateAgentIdentityRequest{Email: "Agent@example.test", Phone: "+12025550123"}
	var saved AgentIdentityResponse
	testutil.Call(t, testHandler.UpdateAgentIdentity, identityRequest(t, http.MethodPut, id, input)).Want(http.StatusOK).JSON(&saved)
	var got AgentIdentityResponse
	testutil.Call(t, testHandler.GetAgentIdentity, identityRequest(t, http.MethodGet, id, nil)).Want(http.StatusOK).JSON(&got)
	if got != saved || got.AgentID != id || got.Email != "agent@example.test" {
		t.Fatalf("identity round trip = %+v; saved %+v", got, saved)
	}
	var actor, details string
	if err := testPool.QueryRow(context.Background(), `SELECT actor_id::text, details::text FROM activity_log WHERE workspace_id=$1 AND action='agent_identity_updated' AND details->>'agent_id'=$2 ORDER BY created_at DESC LIMIT 1`, testWorkspaceID, id).Scan(&actor, &details); err != nil {
		t.Fatal(err)
	}
	if actor != testUserID || strings.Contains(details, input.Phone) || strings.Contains(details, "example.test") || !json.Valid([]byte(details)) {
		t.Fatalf("unexpected audit record: actor=%s details=%s", actor, details)
	}
}

func TestNormalizeAgentIdentityRejectsInvalidValues(t *testing.T) {
	for _, request := range []updateAgentIdentityRequest{
		{Email: "Cue <cue@example.com>"},
		{Phone: "1380013800"},
	} {
		if _, err := normalizeAgentIdentity(request); err == nil {
			t.Fatalf("normalizeAgentIdentity(%+v) unexpectedly succeeded", request)
		}
	}
}
