package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestUploadCleanupKeepsRestoredOwner(t *testing.T) {
	// Given
	fx := newUploadCleanupFixture(t)
	issue := fx.f.Issue(t, "Restored", testutil.Cols{"status": "cancelled", "updated_at": fx.old})
	_, key := fx.upload(t, testutil.Cols{"issue_id": issue})
	fx.f.Exec(t, `UPDATE issue SET status = 'todo' WHERE id = $1`, issue)
	fx.options.Apply = true

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if report.Deleted != 0 {
		t.Fatal("restored issue lost its attachment")
	}
	if _, err := os.Stat(fx.store.GetFilePath(key)); err != nil {
		t.Fatal(err)
	}
}

func TestUploadCleanupCollectsCustomClosedStatus(t *testing.T) {
	// Given
	fx := newUploadCleanupFixture(t)
	fx.f.Insert(t, "issue_status", testutil.Cols{"workspace_id": fx.f.WorkspaceID,
		"key": "discarded", "name": "Discarded", "category": "closed", "color": "#999999"})
	issue := fx.f.Issue(t, "Discarded", testutil.Cols{"status": "discarded", "updated_at": fx.old})
	fx.upload(t, testutil.Cols{"issue_id": issue})

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("custom closed status not collected: %+v", report)
	}
}

func TestUploadCleanupProtectsReferencedOrphanAndCollectsOnlyOldOrphans(t *testing.T) {
	// Given
	fx := newUploadCleanupFixture(t)
	prefix := "workspaces/" + fx.f.WorkspaceID + "/"
	for _, name := range []string{"referenced.png", "old.bin", "recent.bin"} {
		key := prefix + name
		link, err := fx.store.Upload(context.Background(), key, []byte("payload"), "text/plain", "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "recent.bin" {
			if err := os.Chtimes(fx.store.GetFilePath(key), fx.old, fx.old); err != nil {
				t.Fatal(err)
			}
		}
		if name == "referenced.png" {
			fx.f.Issue(t, "Reference", testutil.Cols{"description": "<img src=\"" + link + "\">"})
		}
	}
	fx.options.Apply = true

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if report.Deleted != 1 || len(report.Candidates) != 1 || report.Candidates[0].Key != prefix+"old.bin" {
		t.Fatalf("incorrect orphan selection: %+v", report)
	}
}

func TestUploadCleanupRetainsUploadsExactlyAtCutoff(t *testing.T) {
	// Given
	fx := newUploadCleanupFixture(t)
	fx.options.Cutoff = fx.old.Truncate(time.Microsecond)
	fx.upload(t, testutil.Cols{"created_at": fx.options.Cutoff})

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates) != 0 {
		t.Fatal("cutoff boundary upload was collected")
	}
}
