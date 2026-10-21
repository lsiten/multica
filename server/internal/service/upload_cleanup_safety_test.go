package service

import (
	"context"
	"os"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestUploadCleanupPreservesCancelledIssueWithLiveRun(t *testing.T) {
	// Given
	fx := newUploadCleanupFixture(t)
	issue := fx.f.Issue(t, "Cancelled", testutil.Cols{"status": "cancelled", "updated_at": fx.old})
	runtimeID := fx.f.Runtime(t, "Cleanup")
	fx.f.Task(t, fx.agentID, testutil.Cols{"issue_id": issue, "status": "running", "runtime_id": runtimeID})
	_, key := fx.upload(t, testutil.Cols{"issue_id": issue})
	fx.options.Apply = true

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if report.Deleted != 0 || len(report.Candidates) != 0 {
		t.Fatalf("live run lost its file: %+v", report)
	}
	if _, err := os.Stat(fx.store.GetFilePath(key)); err != nil {
		t.Fatal(err)
	}
}

func TestUploadCleanupRetriesMissingFileMetadata(t *testing.T) {
	// Given: a previous process deleted the object but crashed before commit.
	fx := newUploadCleanupFixture(t)
	_, key := fx.upload(t, testutil.Cols{})
	if err := fx.store.DeleteObject(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	fx.options.Apply = true

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if report.Deleted != 1 {
		t.Fatalf("missing file was not settled: %+v", report)
	}
	var remaining int
	fx.f.QueryRow(t, `SELECT count(*) FROM attachment WHERE workspace_id = $1`, fx.f.WorkspaceID).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("remaining attachments: %d", remaining)
	}
}

func TestUploadCleanupSkipsBusyDatabaseWithoutDeleting(t *testing.T) {
	// Given
	fx := newUploadCleanupFixture(t)
	_, key := fx.upload(t, testutil.Cols{})
	fx.options.Apply = true
	tx, err := fx.cleaner.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `LOCK TABLE attachment IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err == nil {
		t.Fatal("expected a busy round to be skipped")
	}
	if report.Deleted != 0 {
		t.Fatalf("busy round deleted objects: %+v", report)
	}
	if _, err := os.Stat(fx.store.GetFilePath(key)); err != nil {
		t.Fatal(err)
	}
}

func TestUploadCleanupKeepsFailedObjectForRetryAndContinuesBatch(t *testing.T) {
	// Given: a damaged key must never escape the root or lose its metadata.
	fx := newUploadCleanupFixture(t)
	id, _ := fx.upload(t, testutil.Cols{})
	fx.f.Exec(t, `UPDATE attachment SET url = '/uploads/../invalid' WHERE id = $1`, id)
	fx.upload(t, testutil.Cols{})
	fx.options.Apply = true

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err == nil || report.Failed != 1 || report.Deleted == 0 {
		t.Fatalf("failed object blocked the batch: %+v, %v", report, err)
	}
	var remaining int
	fx.f.QueryRow(t, `SELECT count(*) FROM attachment WHERE id = $1`, id).Scan(&remaining)
	if remaining != 1 {
		t.Fatal("failed deletion lost its retry metadata")
	}
}
