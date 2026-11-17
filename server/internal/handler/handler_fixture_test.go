package handler

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var testHandler *Handler
var testPool *pgxpool.Pool
var testUserID string
var testWorkspaceID string
var testRuntimeID string

// dbfx builds the rows these tests act on and removes them again when the test
// that asked for them ends. New tests build fixtures through it rather than
// writing an INSERT and a matching DELETE by hand; see internal/testutil.
var dbfx *testutil.Fixture

const (
	handlerTestEmail         = "handler-test@multica.ai"
	handlerTestName          = "Handler Test User"
	handlerTestWorkspaceSlug = "handler-tests"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	dbConfigured := dbURL != ""
	if !dbConfigured {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		if dbConfigured {
			fmt.Fprintln(os.Stderr, "Failed to configure handler test database from DATABASE_URL")
			os.Exit(1)
		}
		fmt.Printf("Skipping tests: could not connect to database: %v\n", err)
		os.Exit(0)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		if dbConfigured {
			fmt.Fprintln(os.Stderr, "Failed to connect to handler test database configured by DATABASE_URL")
			os.Exit(1)
		}
		fmt.Printf("Skipping tests: database not reachable: %v\n", err)
		os.Exit(0)
	}
	fixtureLock, err := lockHandlerTestFixture(ctx, pool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to lock handler test fixture: %v\n", err)
		pool.Close()
		os.Exit(1)
	}

	queries := db.New(pool)
	hub := realtime.NewHub()
	go hub.Run()
	bus := events.New()
	emailSvc := service.NewEmailService()
	testHandler = New(queries, pool, hub, bus, emailSvc, nil, nil, analytics.NoopClient{}, Config{AllowSignup: true})
	// httptest.NewRequest defaults RemoteAddr to 192.0.2.1, so every webhook
	// test in the suite shares one IP bucket. With the production default
	// (30/min) the budget runs out partway through the suite and unrelated
	// downstream tests see a 429 from the IP gate instead of the response
	// they're asserting. Tests that exercise rate limiting deliberately
	// swap in a tight limiter with t.Cleanup; this generous default keeps
	// the rest of the suite hermetic.
	testHandler.WebhookRateLimiter = NewMemoryWebhookRateLimiter(WebhookRateLimit{Limit: 1_000_000, Window: time.Minute})
	testHandler.WebhookIPRateLimiter = NewMemoryWebhookIPRateLimiter(WebhookRateLimit{Limit: 1_000_000, Window: time.Minute})
	testHandler.WebhookAbsoluteIPRateLimiter = NewMemoryWebhookAbsoluteIPRateLimiter(WebhookRateLimit{Limit: 1_000_000, Window: time.Minute})
	testHandler.InvitationRateLimiters = NewMemoryInvitationRateLimiters(InvitationRateLimits{
		Actor:     SlidingWindowRateLimit{Limit: 1_000_000, Window: time.Minute},
		Workspace: SlidingWindowRateLimit{Limit: 1_000_000, Window: time.Minute},
		Recipient: SlidingWindowRateLimit{Limit: 1_000_000, Window: time.Minute},
	})
	testPool = pool

	testUserID, testWorkspaceID, err = setupHandlerTestFixture(ctx, pool)
	if err != nil {
		fmt.Printf("Failed to set up handler test fixture: %v\n", err)
		fixtureLock.Close(context.Background())
		pool.Close()
		os.Exit(1)
	}
	dbfx = testutil.New(pool, testWorkspaceID, testUserID)

	code := m.Run()
	if err := cleanupHandlerTestFixture(context.Background(), pool); err != nil {
		fmt.Printf("Failed to clean up handler test fixture: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	if err := fixtureLock.Close(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to release handler test fixture lock: %v\n", err)
		code = 1
	}
	pool.Close()
	os.Exit(code)
}

func setupHandlerTestFixture(ctx context.Context, pool *pgxpool.Pool) (string, string, error) {
	if err := cleanupHandlerTestFixture(ctx, pool); err != nil {
		return "", "", err
	}

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, handlerTestName, handlerTestEmail).Scan(&userID); err != nil {
		return "", "", err
	}

	var workspaceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Handler Tests", handlerTestWorkspaceSlug, "Temporary workspace for handler tests", "HAN").Scan(&workspaceID); err != nil {
		return "", "", err
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		return "", "", err
	}

	var runtimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, NULL, $2, 'cloud', $3, 'online', $4, '{}'::jsonb, $5, now())
		RETURNING id
	`, workspaceID, "Handler Test Runtime", "handler_test_runtime", "Handler test runtime", userID).Scan(&runtimeID); err != nil {
		return "", "", err
	}
	testRuntimeID = runtimeID

	var seededAgentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, permission_mode, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 'public_to', 1, $4)
		RETURNING id
	`, workspaceID, "Handler Test Agent", runtimeID, userID).Scan(&seededAgentID); err != nil {
		return "", "", err
	}
	// MUL-3963: the seeded workspace-visible agent is invocable by workspace
	// members and A2A triggers, so seed its workspace invocation target.
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_invocation_target (agent_id, target_type, target_id)
		VALUES ($1, 'workspace', $2)
		ON CONFLICT (agent_id, target_type, target_id) DO NOTHING
	`, seededAgentID, workspaceID); err != nil {
		return "", "", err
	}

	return userID, workspaceID, nil
}

func cleanupHandlerTestFixture(ctx context.Context, pool *pgxpool.Pool) error {
	var hasClientUsageTable bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('client_usage_daily') IS NOT NULL`).Scan(&hasClientUsageTable); err != nil {
		return err
	}
	if hasClientUsageTable {
		if _, err := pool.Exec(ctx, `DELETE FROM client_usage_daily WHERE user_id IN (SELECT id FROM "user" WHERE email = $1)`, handlerTestEmail); err != nil {
			return err
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, handlerTestWorkspaceSlug); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, handlerTestEmail); err != nil {
		return err
	}
	return nil
}
