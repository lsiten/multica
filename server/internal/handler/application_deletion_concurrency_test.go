package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationCreateWaitsForWorkspaceDeletionFence(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	for _, removeParent := range []bool{false, true} {
		name := "release parent"
		if removeParent {
			name = "delete parent"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			workspaceID := dbfx.Workspace(t, "application create fence", "application-create-fence")
			projectID := dbfx.Project(t, "application fence project", testutil.Cols{"workspace_id": workspaceID})
			holder, err := testPool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback(context.Background())
			q := testHandler.Queries.WithTx(holder)
			if _, err := q.LockWorkspaceForDelete(ctx, parseUUID(workspaceID)); err != nil {
				t.Fatal(err)
			}
			connection, err := testPool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Release()
			svc := *testHandler.applicationService()
			svc.Transactions = connection
			config := protocol.DefaultApplicationConfig()
			config.Mode = "external"
			config.Port = 4100
			type creation struct {
				view application.View
				err  error
			}
			done := make(chan creation, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				view, err := svc.Create(ctx, parseUUID(workspaceID), application.CreateInput{ProjectID: parseUUID(projectID), Name: "fenced service", Kind: "service", Config: config}, application.Actor{Type: "member", ID: parseUUID(testUserID), UserID: parseUUID(testUserID)})
				done <- creation{view, err}
			}()
			defer func() {
				cancel()
				holder.Rollback(context.Background())
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Error("application writer did not drain")
				}
			}()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if err := testPool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1))>0", connection.Conn().PgConn().PID()).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case result := <-done:
					t.Fatalf("create bypassed workspace fence: %+v", result)
				case <-ctx.Done():
					t.Fatal("application writer never reached workspace fence")
				case <-ticker.C:
				}
			}
			if removeParent {
				if err := q.DeleteProject(ctx, db.DeleteProjectParams{ID: parseUUID(projectID), WorkspaceID: parseUUID(workspaceID)}); err != nil {
					t.Fatal(err)
				}
				if err := q.DeleteWorkspace(ctx, parseUUID(workspaceID)); err != nil {
					t.Fatal(err)
				}
				if err := holder.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			} else if err := holder.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var result creation
			select {
			case result = <-done:
			case <-ctx.Done():
				t.Fatal("application writer did not finish after fence release")
			}
			if removeParent {
				if !errors.Is(result.err, application.ErrNotFound) {
					t.Fatalf("deleted parent accepted an application: %+v", result)
				}
				var count int
				if err := testPool.QueryRow(ctx, "SELECT count(*) FROM application WHERE workspace_id=$1", workspaceID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("orphan application count=%d error=%v", count, err)
				}
			} else {
				if result.err != nil {
					t.Fatal(result.err)
				}
				dbfx.Cleanup(t, "DELETE FROM application WHERE id=$1", result.view.ID)
				dbfx.Cleanup(t, "DELETE FROM application_revision WHERE application_id=$1", result.view.ID)
			}
		})
	}
}
