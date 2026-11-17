package handler

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler binaries share fixed fixture identities, including secondary test
// workspaces. Hold a database-scoped session lock through both cleanup passes
// so another binary cannot erase an active suite's rows. A separate connection
// leaves the test pool available; process exit releases the lock automatically.
func lockHandlerTestFixture(ctx context.Context, pool *pgxpool.Pool) (*pgx.Conn, error) {
	conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(0x4d554c48414e444c)); err != nil {
		conn.Close(context.Background())
		return nil, err
	}
	return conn, nil
}
