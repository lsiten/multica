package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func privateAgentTestFixture(t *testing.T) (agentID, ownerID, memberID string) {
	t.Helper()
	ctx := context.Background()
	// A public runtime isolates agent access from the runtime owner fence.
	runtimeID := handlerTestRuntimeID(t)
	var visibility string
	if err := testPool.QueryRow(ctx, "SELECT visibility FROM agent_runtime WHERE id = $1", runtimeID).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, "UPDATE agent_runtime SET visibility = 'public' WHERE id = $1", runtimeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), "UPDATE agent_runtime SET visibility = $1 WHERE id = $2", visibility, runtimeID); err != nil {
			t.Error(err)
		}
	})
	ownerID = dbfx.User(t, "Private Agent Owner", "private-agent-owner-"+uuid.NewString()+"@multica.test")
	dbfx.Member(t, testWorkspaceID, ownerID, "member")
	memberID = dbfx.User(t, "Plain Member", "plain-member-"+uuid.NewString()+"@multica.test")
	dbfx.Member(t, testWorkspaceID, memberID, "member")

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, 'private-access-test-agent', '', 'cloud', '{}'::jsonb,
		        $2, 'private', 1, $3, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, runtimeID, ownerID).Scan(&agentID); err != nil {
		t.Fatalf("create private agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE id = $1`, agentID)
	})

	return agentID, ownerID, memberID
}
