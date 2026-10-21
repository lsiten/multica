package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
)

type uploadCleanupFixture struct {
	f       *testutil.Fixture
	store   *storage.LocalStorage
	cleaner UploadCleaner
	options UploadCleanupOptions
	old     time.Time
	agentID string
}

func newUploadCleanupFixture(t *testing.T) uploadCleanupFixture {
	t.Helper()
	pool := sharedTestPool(t)
	f := testutil.New(pool, "", "")
	f.UserID = f.User(t, "Cleanup", uuid.NewString()+"@cleanup.test")
	f.WorkspaceID = f.Workspace(t, "Cleanup", "cleanup-"+uuid.NewString())
	agentID := f.Agent(t, "Cleanup", "")
	t.Setenv("LOCAL_UPLOAD_DIR", t.TempDir())
	t.Setenv("LOCAL_UPLOAD_BASE_URL", "https://uploads.example.test")
	store := storage.NewLocalStorageFromEnv()
	if store == nil {
		t.Fatal("storage unavailable")
	}
	ws, err := util.ParseUUID(f.WorkspaceID)
	if err := err; err != nil {
		t.Fatal(err)
	}
	return uploadCleanupFixture{f: f, store: store, cleaner: UploadCleaner{Pool: pool, Storage: store},
		options: UploadCleanupOptions{Cutoff: time.Now().Add(-7 * 24 * time.Hour), Limit: 100, WorkspaceID: ws},
		old:     time.Now().Add(-10 * 24 * time.Hour), agentID: agentID}
}

func (f uploadCleanupFixture) upload(t *testing.T, columns testutil.Cols) (string, string) {
	t.Helper()
	id := uuid.NewString()
	key := "workspaces/" + f.f.WorkspaceID + "/" + id + ".txt"
	link, err := f.store.Upload(context.Background(), key, []byte("payload"), "text/plain", "file.txt")
	if err := err; err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{f.store.GetFilePath(key), f.store.GetFilePath(key) + ".meta.json"} {
		if err := os.Chtimes(file, f.old, f.old); err != nil {
			t.Fatal(err)
		}
	}
	cols := testutil.Cols{"id": id, "workspace_id": f.f.WorkspaceID, "uploader_type": "member",
		"uploader_id": f.f.UserID, "filename": "file.txt", "url": link, "content_type": "text/plain",
		"size_bytes": 7, "created_at": f.old}
	for name, value := range columns {
		cols[name] = value
	}
	f.f.Insert(t, "attachment", cols)
	return id, key
}

func TestUploadCleanupOwnerRetention(t *testing.T) {
	for _, tc := range []struct {
		name, kind, status string
		recent             bool
		want               int
	}{
		{"cancelled issue", "issue", "cancelled", false, 1},
		{"active issue", "issue", "in_progress", false, 0},
		{"completed issue", "issue", "done", false, 0},
		{"recent cancellation", "issue", "cancelled", true, 0},
		{"archived chat", "chat", "archived", false, 1},
		{"active chat", "chat", "active", false, 0},
		{"recent archive", "chat", "archived", true, 0},
		{"unbound upload", "unbound", "", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			fx := newUploadCleanupFixture(t)
			updated := fx.old
			if tc.recent {
				updated = time.Now()
			}
			columns := testutil.Cols{}
			switch tc.kind {
			case "issue":
				columns["issue_id"] = fx.f.Issue(t, "Owner", testutil.Cols{"status": tc.status, "updated_at": updated})
			case "chat":
				session := fx.f.Insert(t, "chat_session", testutil.Cols{
					"workspace_id": fx.f.WorkspaceID, "agent_id": fx.agentID, "creator_id": fx.f.UserID, "status": tc.status, "updated_at": updated})
				columns["chat_session_id"] = session
				columns["chat_message_id"] = fx.f.Insert(t, "chat_message", testutil.Cols{
					"chat_session_id": session, "role": "user", "content": "File"})
			case "unbound":
			}
			fx.upload(t, columns)

			// When
			report, err := fx.cleaner.Sweep(context.Background(), fx.options)

			// Then
			if err := err; err != nil {
				t.Fatal(err)
			}
			if len(report.Candidates) != tc.want {
				t.Fatalf("unexpected length: %d", len(report.Candidates))
			}
			if report.Deleted != 0 {
				t.Fatalf("expected zero: %v", report.Deleted)
			}
		})
	}
}

func TestUploadCleanupPreservesSharedReferences(t *testing.T) {
	for _, source := range []string{"issue_url", "attachment_id", "avatar", "version", "draft", "skill_file"} {
		t.Run(source, func(t *testing.T) {
			// Given
			fx := newUploadCleanupFixture(t)
			cancelled := fx.f.Issue(t, "Cancelled", testutil.Cols{"status": "cancelled", "updated_at": fx.old})
			id, key := fx.upload(t, testutil.Cols{"issue_id": cancelled})
			link := fx.store.ObjectURL(key)
			switch source {
			case "issue_url":
				fx.f.Issue(t, "Active", testutil.Cols{"description": fmt.Sprintf("![shared](%s)", link)})
			case "attachment_id":
				fx.f.Issue(t, "Active", testutil.Cols{"description": "/api/attachments/" + id + "/content"})
			case "avatar":
				fx.f.Exec(t, `UPDATE workspace SET avatar_url = $1 WHERE id = $2`, link, fx.f.WorkspaceID)
			case "version":
				active := fx.f.Issue(t, "Active")
				fx.f.Insert(t, "document_content_version", testutil.Cols{"workspace_id": fx.f.WorkspaceID,
					"owner_type": "issue", "owner_id": active, "revision": 1, "content_type": "text/plain",
					"content": link, "actor_type": "member", "actor_id": fx.f.UserID})
			case "draft":
				session := fx.f.Insert(t, "chat_session", testutil.Cols{"workspace_id": fx.f.WorkspaceID, "agent_id": fx.agentID, "creator_id": fx.f.UserID})
				fx.f.Insert(t, "chat_draft_restore", testutil.Cols{"id": uuid.NewString(), "chat_session_id": session, "task_id": uuid.NewString(),
					"content": "restored", "attachment_ids": []string{id}})
			case "skill_file":
				skill := fx.f.Insert(t, "skill", testutil.Cols{"workspace_id": fx.f.WorkspaceID, "name": "Reference", "created_by": fx.f.UserID})
				fx.f.Insert(t, "skill_file", testutil.Cols{"skill_id": skill, "path": "reference.md", "content": link})
			}
			fx.options.Apply = true

			// When
			report, err := fx.cleaner.Sweep(context.Background(), fx.options)

			// Then
			if err := err; err != nil {
				t.Fatal(err)
			}
			if len(report.Candidates) != 0 {
				t.Fatalf("expected empty collection: %v", report.Candidates)
			}
			if _, err := os.Stat(fx.store.GetFilePath(key)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUploadCleanupAppliesBoundedDeletionAndRemovesAttachmentRows(t *testing.T) {
	// Given
	fx := newUploadCleanupFixture(t)
	for range 2 {
		fx.upload(t, testutil.Cols{})
	}
	fx.options.Apply, fx.options.Limit = true, 1

	// When
	report, err := fx.cleaner.Sweep(context.Background(), fx.options)

	// Then
	if err := err; err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates) != 2 {
		t.Fatalf("unexpected length: %d", len(report.Candidates))
	}
	if got, want := report.Deleted, 1; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	var remaining int
	fx.f.QueryRow(t, `SELECT count(*) FROM attachment WHERE workspace_id = $1`, fx.f.WorkspaceID).Scan(&remaining)
	if got, want := remaining, 1; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := os.Stat(fx.store.GetFilePath(report.Candidates[0].Key)); !os.IsNotExist(err) {
		t.Fatalf("file remains: %v", err)
	}
	if _, err := os.Stat(fx.store.GetFilePath(report.Candidates[0].Key) + ".meta.json"); !os.IsNotExist(err) {
		t.Fatalf("file remains: %v", err)
	}
}
