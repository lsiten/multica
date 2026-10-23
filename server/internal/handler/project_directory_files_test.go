package handler

import (
	"context"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestChatProjectDirectoriesWithoutTasks(t *testing.T) {
	agent := createHandlerTestAgent(t, "directory projection", []byte("[]"))
	session := createHandlerTestChatSession(t, agent)
	project := dbfx.Project(t, "directory files")
	dbfx.Exec(t, `UPDATE chat_session SET project_id=$1 WHERE id=$2`, project, session)
	runtime := dbfx.Runtime(t, "directory runtime", testutil.Cols{"daemon_id": "directory-daemon", "runtime_mode": "local"})
	for _, ref := range []string{`{"local_path":"/bound/one","daemon_id":"directory-daemon"}`, `{"local_path":"/bound/two","daemon_id":"offline-daemon"}`} {
		dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "resource_type": "local_directory", "resource_ref": ref, "created_by": testUserID})
	}
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/files?include_attachments=false", nil), "sessionId", session))
	var response ChatSessionFilesResponse
	testutil.Call(t, testHandler.ListChatSessionFiles, req).Want(http.StatusOK).JSON(&response)
	if len(response.Directories) != 2 || len(response.Tasks) != 0 || len(response.ProjectTasks) != 0 {
		t.Fatalf("wrong projection: %+v", response)
	}
	for _, directory := range response.Directories {
		if directory.Path == "/bound/one" && directory.RuntimeID != runtime {
			t.Fatalf("wrong runtime: %+v", directory)
		}
		if directory.Path == "/bound/two" && directory.RuntimeID != "" {
			t.Fatalf("offline resource guessed runtime: %+v", directory)
		}
	}
}

func TestProjectDirectoryRelayRejectsInvalidScopes(t *testing.T) {
	project := dbfx.Project(t, "directory authorization")
	runtime := dbfx.Runtime(t, "bound runtime", testutil.Cols{"daemon_id": "bound-daemon", "runtime_mode": "local"})
	other := dbfx.Runtime(t, "other directory runtime", testutil.Cols{"daemon_id": "other-daemon", "runtime_mode": "local"})
	resource := dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "resource_type": "local_directory", "resource_ref": `{"local_path":"/bound","daemon_id":"bound-daemon"}`, "created_by": testUserID})
	for _, tc := range []struct {
		name, action, path, runtime, task string
		status                            int
	}{
		{"outside path", "workspace_files", "/outside", runtime, "", http.StatusBadRequest},
		{"subdirectory path", "workspace_files", "/bound/sub", runtime, "", http.StatusBadRequest},
		{"wrong runtime", "workspace_files", "/bound", other, "", http.StatusNotFound},
		{"mutation", "edit", "/bound", runtime, "", http.StatusForbidden},
		{"ambiguous scope", "workspace_files", "/bound", runtime, "task", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", map[string]string{"project_resource_id": resource, "task_id": tc.task, "runtime_id": tc.runtime, "path": tc.path, "action": tc.action, "target": "main"}))
			testutil.Call(t, testHandler.ForwardLocalReview, req).Want(tc.status)
		})
	}
}

func TestProjectDirectoryAuthorizationRequiresOwningWorkspaceAndRuntime(t *testing.T) {
	project := dbfx.Project(t, "resource read")
	runtime := dbfx.Runtime(t, "resource read runtime", testutil.Cols{"daemon_id": "read-daemon", "runtime_mode": "local", "status": "online"})
	resource := dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "resource_type": "local_directory", "resource_ref": `{"local_path":"/bound","daemon_id":"read-daemon"}`, "created_by": testUserID})
	foreign := dbfx.Workspace(t, "foreign resource", "foreign-directory-scope")
	dbfx.Member(t, foreign, testUserID, "member")
	member := dbfx.User(t, "other reader", "directory-reader@example.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	for _, tc := range []struct {
		name, user, workspace string
		status                int
	}{
		{"owner", testUserID, testWorkspaceID, http.StatusOK},
		{"other owner", member, testWorkspaceID, http.StatusNotFound},
		{"other workspace", testUserID, foreign, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := withWorkspaceCtx(t, newRequestAsUser(tc.user, http.MethodPost, "/execute", nil), tc.workspace)
			handler := func(w http.ResponseWriter, r *http.Request) {
				path, id, ok := testHandler.authorizeProjectDirectoryRead(w, r, tc.user, resource, runtime, "/bound")
				if ok {
					if path != "/bound" || id != runtime {
						t.Errorf("incorrect authorized scope: %s %s", path, id)
					}
					w.WriteHeader(http.StatusOK)
				}
			}
			testutil.Call(t, handler, req).Want(tc.status)
		})
	}
}

func TestChatProjectFilesRetainInheritedTaskDirectory(t *testing.T) {
	agent := createHandlerTestAgent(t, "inherited directory", []byte("[]"))
	session := createHandlerTestChatSession(t, agent)
	project := dbfx.Project(t, "inherited files")
	dbfx.Exec(t, `UPDATE chat_session SET project_id=$1 WHERE id=$2`, project, session)
	parent := dbfx.Task(t, agent, testutil.Cols{"runtime_id": handlerTestRuntimeID(t), "work_dir": "/inherited/root"})
	child := dbfx.Task(t, agent, testutil.Cols{"runtime_id": handlerTestRuntimeID(t), "chat_session_id": session, "parent_task_id": parent})
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/files?include_attachments=false", nil), "sessionId", session))
	var response ChatSessionFilesResponse
	testutil.Call(t, testHandler.ListChatSessionFiles, req).Want(http.StatusOK).JSON(&response)
	if len(response.Tasks) != 1 || response.Tasks[0].ID != child || response.Tasks[0].WorkDir != "/inherited/root" {
		t.Fatalf("inherited session directory dropped: %+v", response.Tasks)
	}
	if len(response.ProjectTasks) != 1 || response.ProjectTasks[0].ID != child || response.ProjectTasks[0].WorkDir != "/inherited/root" {
		t.Fatalf("inherited project directory dropped: %+v", response.ProjectTasks)
	}
}

func TestChatProjectFilesExplicitProjectOverridesAgentBindings(t *testing.T) {
	agent := createHandlerTestAgent(t, "multi project directories", []byte("[]"))
	session := createHandlerTestChatSession(t, agent)
	projectA := dbfx.Project(t, "directory project A")
	projectB := dbfx.Project(t, "directory project B")
	for _, project := range []string{projectA, projectB} {
		dbfx.Exec(t, `INSERT INTO agent_project_binding (agent_id,project_id,created_by,active) VALUES ($1,$2,$3,TRUE)`, agent, project, testUserID)
	}
	issue := dbfx.Issue(t, "explicit project B", testutil.Cols{"project_id": projectB})
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": handlerTestRuntimeID(t), "status": "completed", "issue_id": issue, "work_dir": "/project-b/issue"})
	otherSession := createHandlerTestChatSession(t, agent)
	dbfx.Exec(t, `UPDATE chat_session SET project_id=$1 WHERE id=$2`, projectB, otherSession)
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": handlerTestRuntimeID(t), "status": "completed", "chat_session_id": otherSession, "work_dir": "/project-b/chat"})
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": handlerTestRuntimeID(t), "status": "completed", "context": `{"type":"quick_create","project_id":"` + projectB + `"}`, "work_dir": "/project-b/context"})
	selectedSession := createHandlerTestChatSession(t, agent)
	dbfx.Exec(t, `UPDATE chat_session SET project_id=$1 WHERE id=$2`, projectA, selectedSession)
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": handlerTestRuntimeID(t), "status": "completed", "issue_id": issue, "chat_session_id": selectedSession, "work_dir": "/project-b/issue-precedence"})
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": handlerTestRuntimeID(t), "status": "completed", "chat_session_id": otherSession, "context": `{"project_id":"` + projectA + `"}`, "work_dir": "/project-b/chat-precedence"})
	unscoped := dbfx.Task(t, agent, testutil.Cols{"runtime_id": handlerTestRuntimeID(t), "status": "completed", "work_dir": "/unscoped/direct"})
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/files?include_attachments=false&project_id="+projectA, nil), "sessionId", session))
	var response ChatSessionFilesResponse
	testutil.Call(t, testHandler.ListChatSessionFiles, req).Want(http.StatusOK).JSON(&response)
	if len(response.ProjectTasks) != 1 || response.ProjectTasks[0].ID != unscoped {
		t.Fatalf("explicit project leaked through agent bindings: %+v", response.ProjectTasks)
	}
}

func TestProjectDirectoryRelayPreservesShallowPaging(t *testing.T) {
	project := dbfx.Project(t, "paged directory")
	runtime := dbfx.Runtime(t, "paged directory runtime", testutil.Cols{"daemon_id": "paged-daemon", "runtime_mode": "local", "status": "online"})
	resource := dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "resource_type": "local_directory", "resource_ref": `{"local_path":"/bound","daemon_id":"paged-daemon"}`, "created_by": testUserID})
	request := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", map[string]any{"project_resource_id": resource, "runtime_id": runtime, "path": "/bound", "action": "workspace_files", "relative_path": "docs", "recursive": false, "offset": 500, "limit": 500}))
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	commands := make(chan protocol.LocalReviewCommand, 1)
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			command := testHandler.localReviewRelay.claim(testWorkspaceID, runtime)
			if command == nil {
				continue
			}
			commands <- *command
			testHandler.localReviewRelay.complete(testWorkspaceID, runtime, command.ID, protocol.LocalReviewResult{ClaimToken: command.ClaimToken, Page: []byte(`{"path":"/bound","entries":[],"recursive":false,"next_offset":500,"has_more":false}`)})
			return
		}
	}()
	testutil.Call(t, testHandler.ForwardLocalReview, request).Want(http.StatusOK)
	select {
	case command := <-commands:
		if command.Recursive == nil || *command.Recursive || command.RelativePath != "docs" || command.Offset != 500 || command.Limit != 500 {
			t.Fatal("directory mode or cursor lost in relay")
		}
	case <-ctx.Done():
		t.Fatal("request did not reach runtime")
	}
}
