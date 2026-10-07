package migrations

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestApplicationOperationDeadlineMigrationBudgetsExistingPreparationAndRetries(t *testing.T) {
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
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE application_operation(id text,workspace_id text,created_at timestamptz);
 CREATE TEMP TABLE application_operation_step(operation_id text,workspace_id text,command jsonb);
 INSERT INTO application_operation VALUES('configured','workspace','2026-10-07 00:00:00+00'),('no-steps','workspace','2026-10-07 00:00:00+00');
 INSERT INTO application_operation_step VALUES('configured','workspace','{"config":{"prepare":[{"timeout_seconds":450}],"health":{"kind":"tcp","timeout_seconds":60},"restart":{"enabled":true,"max_attempts":3,"delay_seconds":5}}}');`); err != nil {
		t.Fatal(err)
	}
	applyMigrationFile(t, ctx, conn, "900534_application_operation_deadline.up.sql")
	var deadline time.Time
	created := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	if err := conn.QueryRow(ctx, "SELECT deadline_at FROM application_operation WHERE id='configured'").Scan(&deadline); err != nil || !deadline.Equal(created.Add(3*time.Hour+21*time.Minute+45*time.Second)) {
		t.Fatalf("existing preparation received an incorrect deadline: %s %v", deadline, err)
	}
	if err := conn.QueryRow(ctx, "SELECT deadline_at FROM application_operation WHERE id='no-steps'").Scan(&deadline); err != nil || !deadline.Equal(created.Add(3*time.Hour)) {
		t.Fatalf("empty operation received an unbounded or invalid deadline: %s %v", deadline, err)
	}
	if _, err := conn.Exec(ctx, "UPDATE application_operation SET deadline_at=deadline_at+interval '1 hour' WHERE id='configured'"); err != nil {
		t.Fatal(err)
	}
	applyMigrationFile(t, ctx, conn, "900534_application_operation_deadline.up.sql")
	if err := conn.QueryRow(ctx, "SELECT deadline_at FROM application_operation WHERE id='configured'").Scan(&deadline); err != nil || !deadline.Equal(created.Add(4*time.Hour+21*time.Minute+45*time.Second)) {
		t.Fatalf("repeat migration overwrote the persisted deadline: %s %v", deadline, err)
	}
	applyMigrationFile(t, ctx, conn, "900534_application_operation_deadline.down.sql")
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM application_operation").Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback removed operation history: %d %v", count, err)
	}
}
