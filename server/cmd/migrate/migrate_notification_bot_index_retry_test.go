package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNotificationBotConcurrentIndexesRecoverInvalidBuilds(t *testing.T) {
	for _, tc := range []struct {
		version string
		index   string
		table   string
	}{
		{"452_notification_bot_owner_index", "notification_bot_owner_idx", "notification_bot"},
		{"453_notification_bot_delivery_unique", "notification_bot_delivery_unique_idx", "notification_bot_delivery"},
		{"454_notification_bot_delivery_ready", "notification_bot_delivery_ready_idx", "notification_bot_delivery"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			// Given a failed concurrent build with the production index name.
			admin := openTestPool(t)
			schema := "notification_retry_" + uuid.NewString()
			schemaName := pgx.Identifier{schema}.Sanitize()
			if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+schemaName); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if _, err := admin.Exec(ctx, "DROP SCHEMA "+schemaName+" CASCADE"); err != nil {
					t.Error(err)
				}
			})
			config := admin.Config()
			config.ConnConfig.RuntimeParams["search_path"] = schemaName
			pool, err := pgxpool.NewWithConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if _, err := pool.Exec(t.Context(), `
				CREATE TABLE notification_bot (workspace_id uuid, user_id uuid);
				INSERT INTO notification_bot SELECT gen_random_uuid(), gen_random_uuid() FROM generate_series(1, 2);
				CREATE TABLE notification_bot_delivery (bot_id uuid, inbox_id uuid, next_attempt_at timestamptz, completed_at timestamptz);
				INSERT INTO notification_bot_delivery SELECT gen_random_uuid(), gen_random_uuid(), now(), NULL FROM generate_series(1, 2);
			`); err != nil {
				t.Fatal(err)
			}
			_, err = pool.Exec(t.Context(), "CREATE UNIQUE INDEX CONCURRENTLY "+pgx.Identifier{tc.index}.Sanitize()+
				" ON "+pgx.Identifier{tc.table}.Sanitize()+" ((1))")
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
				t.Fatalf("expected a failed unique build, got %v", err)
			}
			assertIndexValidity(t, pool, schema, tc.index, false)

			// When the unchanged published migration retries with production hooks.
			err = runMigrations(t.Context(), pool, runOptions{
				Direction:             "up",
				Files:                 []string{filepath.Join("..", "..", "migrations", tc.version+".up.sql")},
				SchemaMigrationsTable: schema + ".schema_migrations",
				AdvisoryLockKey:       int64(rand.Uint64()>>1) | 1,
				Hooks:                 preMigrationHooks,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Then the failed relation is replaced by a usable production index.
			assertIndexValidity(t, pool, schema, tc.index, true)
		})
	}
}
