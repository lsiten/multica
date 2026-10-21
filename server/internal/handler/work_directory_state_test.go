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

func TestRuntimeWorkDirectoryStateGuardsActivePathsAndDaemonOwnership(t *testing.T) {
	runtime := dbfx.Runtime(t, "active-directory-state", testutil.Cols{"daemon_id": "directory-daemon", "runtime_mode": "local"})
	agent := dbfx.Agent(t, "active-directory-agent", runtime)
	retained := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "completed", "work_dir": "/shared"})
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "running", "work_dir": "/shared"})
	for _, tc := range []struct {
		daemon string
		status int
	}{{"other-daemon", http.StatusForbidden}, {"directory-daemon", http.StatusOK}} {
		r := withURLParam(newDaemonTokenRequest(http.MethodPost, "/work-directories", []protocol.WorkDirectoryState{{Path: "/shared", Missing: true}}, testWorkspaceID, tc.daemon), "runtimeId", runtime)
		testutil.Call(t, testHandler.UpdateRuntimeWorkDirectories, r).Want(tc.status)
	}
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(retained))
	if err != nil || task.WorkDirMissing {
		t.Fatalf("active shared directory was hidden: %+v, %v", task, err)
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
