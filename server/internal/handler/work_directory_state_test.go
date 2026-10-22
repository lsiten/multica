package handler

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRuntimeWorkDirectoryStatePersistsAndFiltersChatFiles(t *testing.T) {
	runtime := dbfx.Runtime(t, "directory-state", testutil.Cols{"daemon_id": "directory-daemon", "runtime_mode": "local"})
	otherRuntime := dbfx.Runtime(t, "other-directory-state", testutil.Cols{"daemon_id": "other-daemon", "runtime_mode": "local"})
	agent := dbfx.Agent(t, "directory-state-agent", runtime)
	project := dbfx.Project(t, "Directory state project")
	chat := createHandlerTestChatSession(t, agent)
	dbfx.Exec(t, `UPDATE chat_session SET project_id = $1 WHERE id = $2`, project, chat)
	removed := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": chat, "status": "completed", "work_dir": "/removed"})
	child := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": chat, "status": "completed", "parent_task_id": removed})
	retained := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": chat, "status": "completed", "work_dir": "/retained"})
	foreign := dbfx.Task(t, agent, testutil.Cols{"runtime_id": otherRuntime, "status": "completed", "work_dir": "/removed"})
	states := []protocol.WorkDirectoryState{{Path: "/removed", Missing: true}}
	r := withURLParam(newDaemonTokenRequest(http.MethodPost, "/work-directories", states, testWorkspaceID, "directory-daemon"), "runtimeId", runtime)
	testutil.Call(t, testHandler.UpdateRuntimeWorkDirectories, r).Want(http.StatusOK)
	for _, tc := range []struct {
		id      string
		missing bool
	}{{removed, true}, {retained, false}, {foreign, false}} {
		task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(tc.id))
		if err != nil || task.WorkDirMissing != tc.missing {
			t.Fatalf("persisted directory state for %s: missing=%v, err=%v", tc.id, task.WorkDirMissing, err)
		}
	}
	request := withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/files", nil), "sessionId", chat))
	var files ChatSessionFilesResponse
	testutil.Call(t, testHandler.ListChatSessionFiles, request).Want(http.StatusOK).JSON(&files)
	for _, tasks := range [][]ChatSessionFileTaskResponse{files.Tasks, files.ProjectTasks} {
		if len(tasks) != 1 || tasks[0].ID != retained {
			t.Fatalf("removed task %s or child %s reappeared in file projection: %+v", removed, child, tasks)
		}
	}
	rows, err := testHandler.Queries.ListLocalReviewWorktrees(t.Context(), db.ListLocalReviewWorktreesParams{WorkspaceID: parseUUID(testWorkspaceID), ReaderID: parseUUID(testUserID), AgentID: parseUUID(agent)})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if uuidToString(row.TaskID) == removed {
			t.Fatal("removed directory remained in the worktree inventory")
		}
	}
	states[0].Missing = false
	r = withURLParam(newDaemonTokenRequest(http.MethodPost, "/work-directories", states, testWorkspaceID, "directory-daemon"), "runtimeId", runtime)
	testutil.Call(t, testHandler.UpdateRuntimeWorkDirectories, r).Want(http.StatusOK)
	testutil.Call(t, testHandler.ListChatSessionFiles, request).Want(http.StatusOK).JSON(&files)
	if len(files.Tasks) != 3 || len(files.ProjectTasks) != 3 {
		t.Fatalf("restored directory did not reappear: %+v", files)
	}
}

func TestRuntimeWorkDirectoryStateRecordsMissingActivePathsAndGuardsDaemonOwnership(t *testing.T) {
	runtime := dbfx.Runtime(t, "active-directory-state", testutil.Cols{"daemon_id": "directory-daemon", "runtime_mode": "local"})
	agent := dbfx.Agent(t, "active-directory-agent", runtime)
	project := dbfx.Project(t, "Active directory state project")
	chat := createHandlerTestChatSession(t, agent)
	dbfx.Exec(t, `UPDATE chat_session SET project_id = $1 WHERE id = $2`, project, chat)
	retained := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": chat, "status": "completed", "work_dir": "/shared"})
	active := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": chat, "status": "running", "work_dir": "/shared"})
	for _, tc := range []struct {
		daemon string
		status int
	}{{"other-daemon", http.StatusForbidden}, {"directory-daemon", http.StatusOK}} {
		r := withURLParam(newDaemonTokenRequest(http.MethodPost, "/work-directories", []protocol.WorkDirectoryState{{Path: "/shared", Missing: true}}, testWorkspaceID, tc.daemon), "runtimeId", runtime)
		testutil.Call(t, testHandler.UpdateRuntimeWorkDirectories, r).Want(tc.status)
	}
	for _, id := range []string{retained, active} {
		task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(id))
		if err != nil || !task.WorkDirMissing {
			t.Fatalf("missing directory remained available for task %s: %+v, %v", id, task, err)
		}
	}
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(active))
	if err != nil || task.Status != "running" {
		t.Fatalf("availability report changed active task lifecycle: %+v, %v", task, err)
	}
	filesRequest := withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/files", nil), "sessionId", chat))
	var files ChatSessionFilesResponse
	testutil.Call(t, testHandler.ListChatSessionFiles, filesRequest).Want(http.StatusOK).JSON(&files)
	if len(files.Tasks) != 0 || len(files.ProjectTasks) != 0 {
		t.Fatalf("missing active directory remained in file projection: %+v", files)
	}
	r := withURLParam(newDaemonTokenRequest(http.MethodPost, "/work-directories", []protocol.WorkDirectoryState{{Path: "/shared", Missing: false}}, testWorkspaceID, "directory-daemon"), "runtimeId", runtime)
	testutil.Call(t, testHandler.UpdateRuntimeWorkDirectories, r).Want(http.StatusOK)
	task, err = testHandler.Queries.GetAgentTask(t.Context(), parseUUID(active))
	if err != nil || task.WorkDirMissing || task.Status != "running" {
		t.Fatalf("restored active directory did not recover: %+v, %v", task, err)
	}
	testutil.Call(t, testHandler.ListChatSessionFiles, filesRequest).Want(http.StatusOK).JSON(&files)
	if len(files.Tasks) != 2 || len(files.ProjectTasks) != 2 {
		t.Fatalf("restored active directory remained hidden: %+v", files)
	}
	request := withURLParam(newDaemonTokenRequest(http.MethodGet, "/work-directories?after=/shared", nil, testWorkspaceID, "directory-daemon"), "runtimeId", runtime)
	var page protocol.RuntimeWorkDirectoryPage
	testutil.Call(t, testHandler.ListRuntimeWorkDirectories, request).Want(http.StatusOK).JSON(&page)
	if len(page.Paths) != 0 {
		t.Fatalf("directory cursor was ignored: %+v", page)
	}
	for _, body := range []string{`[]`, `[{"path":"/shared"}]`, `[{"path":"/shared","missing":true,"extra":1}]`, `[{"path":"/shared","missing":true}] {}`} {
		r := withURLParam(newDaemonTokenRequest(http.MethodPost, "/work-directories", nil, testWorkspaceID, "directory-daemon"), "runtimeId", runtime)
		r.Body = io.NopCloser(strings.NewReader(body))
		testutil.Call(t, testHandler.UpdateRuntimeWorkDirectories, r).Want(http.StatusBadRequest)
	}
}
