package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	util "github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// withWorkspaceCtx scopes a request to a (possibly foreign) workspace the same
// way the auth middleware would, so a handler's ctxWorkspaceID sees it.
func withWorkspaceCtx(t *testing.T, req *http.Request, workspaceID string) *http.Request {
	t.Helper()
	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(workspaceID),
	})
	if err != nil {
		t.Fatalf("load member row: %v", err)
	}
	return req.WithContext(middleware.SetMemberContext(req.Context(), workspaceID, memberRow))
}

// TestListChatSessionFiles is the happy path: the chat file browser's
// projection returns the session's project, its attachments, and its tasks
// (with work_dirs), all resolved from the session and scoped to its workspace.
func TestListChatSessionFiles(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}

	agentID := createHandlerTestAgent(t, "SessionFilesAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	// Bind the session to a project so the file browser can label it.
	projectID := dbfx.Project(t, "Session Files Project")
	dbfx.Exec(t, `UPDATE chat_session SET project_id = $1 WHERE id = $2`, projectID, sessionID)

	attachmentID := dbfx.Insert(t, "attachment", testutil.Cols{
		"workspace_id":    testWorkspaceID,
		"chat_session_id": sessionID,
		"uploader_type":   "member",
		"uploader_id":     testUserID,
		"filename":        "report.pdf",
		"url":             "https://cdn.example/report.pdf",
		"content_type":    "application/pdf",
		"size_bytes":      100,
	})

	// A task with a work_dir is the project's checked-out files.
	dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id":      handlerTestRuntimeID(t),
		"chat_session_id": sessionID,
		"work_dir":        "/work",
	})

	req := newRequest("GET", "/api/chat/sessions/"+sessionID+"/files", nil)
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.ListChatSessionFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChatSessionFilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if resp.WorkspaceID != testWorkspaceID {
		t.Fatalf("workspace_id: want %s, got %s", testWorkspaceID, resp.WorkspaceID)
	}
	if resp.ProjectID == nil || *resp.ProjectID != projectID {
		t.Fatalf("project_id: want %s, got %v", projectID, resp.ProjectID)
	}
	if len(resp.Attachments) != 1 || resp.Attachments[0].ID != attachmentID {
		t.Fatalf("attachments: want %s, got %d", attachmentID, len(resp.Attachments))
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("tasks: want 1, got %d", len(resp.Tasks))
	}
	if resp.Tasks[0].WorkDir != "/work" {
		t.Fatalf("work_dir: want /work, got %q", resp.Tasks[0].WorkDir)
	}
	if len(resp.Projects) != 1 || resp.Projects[0].ID != projectID {
		t.Fatalf("projects: want selected project %s, got %+v", projectID, resp.Projects)
	}
	if len(resp.ProjectTasks) != 1 || resp.ProjectTasks[0].WorkDir != "/work" {
		t.Fatalf("project_tasks: want /work, got %+v", resp.ProjectTasks)
	}
	if len(resp.ProjectAttachments) != 1 || resp.ProjectAttachments[0].ID != attachmentID {
		t.Fatalf("project_attachments: want %s, got %+v", attachmentID, resp.ProjectAttachments)
	}
}

func TestListChatSessionFiles_DurableWorkDirFallback(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}

	agentID := createHandlerTestAgent(t, "SessionFilesDurableAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)
	projectID := dbfx.Project(t, "Durable Files Project")
	dbfx.Exec(t, `UPDATE chat_session SET project_id = $1 WHERE id = $2`, projectID, sessionID)
	dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id":       handlerTestRuntimeID(t),
		"chat_session_id":  sessionID,
		"durable_work_dir": "/archive/worktree",
		"status":           "completed",
	})

	req := newRequest("GET", "/api/chat/sessions/"+sessionID+"/files", nil)
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.ListChatSessionFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChatSessionFilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if len(resp.Tasks) != 1 || resp.Tasks[0].WorkDir != "/archive/worktree" {
		t.Fatalf("tasks durable fallback: got %+v", resp.Tasks)
	}
	if len(resp.ProjectTasks) != 1 || resp.ProjectTasks[0].WorkDir != "/archive/worktree" {
		t.Fatalf("project_tasks durable fallback: got %+v", resp.ProjectTasks)
	}
}

// TestListChatSessionFiles_WorkspaceIsolation guards the tenant boundary: a
// request scoped to a different workspace cannot read the session's files even
// though it knows the session id — the projection resolves the session through
// a workspace-guarded lookup and rejects it as not found.
func TestListChatSessionFiles_WorkspaceIsolation(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}

	agentID := createHandlerTestAgent(t, "SessionFilesAgent2", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)
	dbfx.Insert(t, "attachment", testutil.Cols{
		"workspace_id":    testWorkspaceID,
		"chat_session_id": sessionID,
		"uploader_type":   "member",
		"uploader_id":     testUserID,
		"filename":        "secret.pdf",
		"url":             "https://cdn.example/secret.pdf",
		"content_type":    "application/pdf",
		"size_bytes":      100,
	})

	otherWorkspaceID := dbfx.Workspace(t, "Other Catalog", "other-catalog")
	dbfx.Member(t, otherWorkspaceID, testUserID, "member")

	req := newRequest("GET", "/api/chat/sessions/"+sessionID+"/files", nil)
	req = withURLParam(req, "sessionId", sessionID)
	req = withWorkspaceCtx(t, req, otherWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListChatSessionFiles(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListChatSessionFiles_ProjectReposWithoutTask covers the projection's
// project-repo resolution: a chat about a project that has no task worktree
// still returns the project's configured repositories (project resource ->
// workspace repos fallback), so the file browser can list them as references.
func TestListChatSessionFiles_ProjectReposWithoutTask(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}

	agentID := createHandlerTestAgent(t, "SessionFilesReposAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	projectID := dbfx.Project(t, "Repos Project")
	dbfx.Exec(t, `UPDATE chat_session SET project_id = $1 WHERE id = $2`, projectID, sessionID)
	dbfx.Insert(t, "project_resource", testutil.Cols{
		"project_id":    projectID,
		"workspace_id":  testWorkspaceID,
		"resource_type": "github_repo",
		"resource_ref":  `{"url":"https://github.com/acme/widgets"}`,
		"position":      0,
	})

	req := newRequest("GET", "/api/chat/sessions/"+sessionID+"/files", nil)
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.ListChatSessionFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChatSessionFilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if len(resp.Repos) != 1 || resp.Repos[0].URL != "https://github.com/acme/widgets" {
		t.Fatalf("repos: want 1 repo at the configured URL, got %+v", resp.Repos)
	}
}
