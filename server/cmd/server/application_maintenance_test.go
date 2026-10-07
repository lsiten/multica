package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestApplicationTicketCleanupIsBoundedAndPreservesUnexpiredLaunches(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	ctx := context.Background()
	endpointID := uuid.NewString()
	prefix := uuid.NewString()
	fx := testutil.New(testPool, testWorkspaceID, testUserID)
	member, err := db.New(testPool).GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(testUserID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	fx.Cleanup(t, "DELETE FROM application_access_ticket WHERE endpoint_id=$1", endpointID)
	inserted, err := testPool.Exec(ctx, `INSERT INTO application_access_ticket(token_hash,endpoint_id,workspace_id,user_id,endpoint_revision,member_id,expires_at)
 SELECT $1 || n::text,$2,$3,$4,1,$6,now()-interval '100 years' FROM generate_series(1,$5) n`, prefix, endpointID, testWorkspaceID, testUserID, applicationTicketPruneBatchSize+1, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inserted.RowsAffected() != applicationTicketPruneBatchSize+1 {
		t.Fatalf("expired ticket fixture count=%d", inserted.RowsAffected())
	}
	queries := db.New(testPool)
	if err := queries.CreateApplicationAccessTicket(ctx, db.CreateApplicationAccessTicketParams{TokenHash: prefix + "valid", EndpointID: parseUUID(endpointID), WorkspaceID: parseUUID(testWorkspaceID), UserID: parseUUID(testUserID), MemberID: member.ID, EndpointRevision: 1}); err != nil {
		t.Fatal(err)
	}
	sweepExpiredApplicationTickets(ctx, queries)
	var expired, valid int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE expires_at<now()),count(*) FILTER (WHERE expires_at>=now()) FROM application_access_ticket WHERE endpoint_id=$1`, endpointID).Scan(&expired, &valid); err != nil {
		t.Fatal(err)
	}
	if expired != 1 || valid != 1 {
		t.Fatalf("cleanup exceeded its budget or removed a valid ticket: expired=%d valid=%d", expired, valid)
	}
	sweepExpiredApplicationTickets(ctx, queries)
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM application_access_ticket WHERE endpoint_id=$1", endpointID).Scan(&valid); err != nil {
		t.Fatal(err)
	}
	if valid != 1 {
		t.Fatalf("subsequent cleanup did not retain exactly the valid launch: %d", valid)
	}
}
