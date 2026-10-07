package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestApplicationAccessMembershipMigrationExpiresUnboundTicketsAndIsRepeatable(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE application_access_ticket (
 token_hash text NOT NULL, endpoint_id uuid NOT NULL, workspace_id uuid NOT NULL,
 user_id uuid NOT NULL, endpoint_revision bigint NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT now()+interval '1 minute'
);
INSERT INTO application_access_ticket(token_hash,endpoint_id,workspace_id,user_id,endpoint_revision)
VALUES('unbound-ticket','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333',1);`); err != nil {
		t.Fatal(err)
	}
	applyMigrationFile(t, ctx, conn, "900533_application_access_membership.up.sql")
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM application_access_ticket").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unbound authorization survived migration: count=%d error=%v", count, err)
	}
	id, err := util.ParseUUID("44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatal(err)
	}
	queries := db.New(conn)
	if err := queries.CreateApplicationAccessTicket(ctx, db.CreateApplicationAccessTicketParams{TokenHash: "bound-ticket", EndpointID: id, WorkspaceID: id, UserID: id, MemberID: id, EndpointRevision: 1}); err != nil {
		t.Fatal(err)
	}
	applyMigrationFile(t, ctx, conn, "900533_application_access_membership.up.sql")
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM application_access_ticket WHERE member_id=$1", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("repeat migration removed a bound authorization: count=%d error=%v", count, err)
	}
	applyMigrationFile(t, ctx, conn, "900533_application_access_membership.down.sql")
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM application_access_ticket").Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback lost unrelated ticket fields: count=%d error=%v", count, err)
	}
	applyMigrationFile(t, ctx, conn, "900533_application_access_membership.up.sql")
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM application_access_ticket").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback authorization was incorrectly rebound: count=%d error=%v", count, err)
	}
}
