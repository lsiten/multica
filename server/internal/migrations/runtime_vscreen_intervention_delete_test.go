package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const runtimeVscreenInterventionDeleteTestSchema = "runtime_vscreen_intervention_delete_test"

func TestRuntimeVscreenInterventionDeleteMigrationRestoresEmptyTable(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to Postgres: %v", err)
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire Postgres connection: %v", err)
	}
	defer conn.Release()

	cleanup := func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+runtimeVscreenInterventionDeleteTestSchema+" CASCADE")
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+runtimeVscreenInterventionDeleteTestSchema); err != nil {
		t.Fatalf("create isolated migration schema: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('search_path', $1, false)`, runtimeVscreenInterventionDeleteTestSchema); err != nil {
		t.Fatalf("set isolated migration search path: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE runtime_vscreen_intervention (id UUID PRIMARY KEY);
		INSERT INTO runtime_vscreen_intervention (id) VALUES (gen_random_uuid());
	`); err != nil {
		t.Fatalf("create intervention table: %v", err)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "900574_runtime_vscreen_intervention_delete.up.sql")
	assertRuntimeVscreenInterventionTableMissing(t, ctx, conn)

	applyMigrationFile(t, ctx, conn.Conn(), "900574_runtime_vscreen_intervention_delete.up.sql")
	assertRuntimeVscreenInterventionTableMissing(t, ctx, conn)

	applyMigrationFile(t, ctx, conn.Conn(), "900574_runtime_vscreen_intervention_delete.down.sql")
	var rows int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM runtime_vscreen_intervention").Scan(&rows); err != nil {
		t.Fatalf("count restored intervention rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("restored intervention table has %d rows, want 0", rows)
	}
}

func assertRuntimeVscreenInterventionTableMissing(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(ctx, `
		SELECT to_regclass($1) IS NOT NULL
	`, runtimeVscreenInterventionDeleteTestSchema+".runtime_vscreen_intervention").Scan(&exists); err != nil {
		t.Fatalf("inspect runtime_vscreen_intervention table: %v", err)
	}
	if exists {
		t.Fatal("runtime_vscreen_intervention table still exists after deletion migration")
	}
}
