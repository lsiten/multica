package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type claimCodeWorkspaceProbe struct {
	Task *struct {
		ID                           string `json:"id"`
		CodeWorkspaceIssueID         string `json:"code_workspace_issue_id"`
		CodeWorkspaceIssueIdentifier string `json:"code_workspace_issue_identifier"`
		CodeWorkspaceWorkDir         string `json:"code_workspace_work_dir"`
		PriorWorkDir                 string `json:"prior_work_dir"`
		PriorSessionID               string `json:"prior_session_id"`
	} `json:"task"`
}

func TestClaimCodeWorkspace_ResolvesRootWithoutSharingSessions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		depth    int
		kind     string
		rootRun  bool
		childRun bool
	}{
		{"root", 0, "task", true, false},
		{"child", 1, "task", true, false},
		{"grandchild", 2, "task", true, false},
		{"requirement", 2, "requirement", true, false},
		{"root_without_run", 0, "task", false, false},
		{"child_before_root_run", 1, "task", false, false},
		{"legacy_child_directory", 2, "task", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			if testHandler == nil {
				t.Fatal("database is required")
			}
			agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, context.Background())
			projectID := dbfx.Project(t, "family project")
			rootID := dbfx.Issue(t, "family root", testutil.Cols{"project_id": projectID, "number": 9801, "kind": tc.kind})
			issueID := rootID
			for range tc.depth {
				issueID = dbfx.Issue(t, "family child", testutil.Cols{"project_id": projectID, "parent_issue_id": issueID})
			}
			if tc.rootRun {
				rootAgentID := dbfx.Agent(t, "root owner", runtimeID)
				dbfx.Task(t, rootAgentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": rootID, "status": "completed", "work_dir": "/tmp/family-root", "session_id": "root-session"})
			}
			if tc.childRun {
				dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": "completed", "work_dir": "/tmp/legacy-child", "session_id": "child-session"})
			}
			taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
			req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, daemonID)
			req = withURLParam(req, "runtimeId", runtimeID)
			// When
			var response claimCodeWorkspaceProbe
			result := testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
			result.JSON(&response)
			// Then
			if response.Task == nil {
				t.Fatal("claim returned no task")
			}
			if response.Task.ID != taskID {
				t.Errorf("response.Task.ID = %q, want %q", response.Task.ID, taskID)
			}
			if response.Task.CodeWorkspaceIssueID != rootID {
				t.Errorf("response.Task.CodeWorkspaceIssueID = %q, want %q", response.Task.CodeWorkspaceIssueID, rootID)
			}
			if response.Task.CodeWorkspaceIssueIdentifier != "HAN-9801" {
				t.Errorf("response.Task.CodeWorkspaceIssueIdentifier = %q, want %q", response.Task.CodeWorkspaceIssueIdentifier, "HAN-9801")
			}
			wantDir := ""
			if tc.rootRun {
				wantDir = "/tmp/family-root"
			}
			if response.Task.CodeWorkspaceWorkDir != wantDir {
				t.Errorf("response.Task.CodeWorkspaceWorkDir = %q, want %q", response.Task.CodeWorkspaceWorkDir, wantDir)
			}
			if tc.childRun {
				if response.Task.PriorWorkDir != "/tmp/legacy-child" {
					t.Errorf("response.Task.PriorWorkDir = %q, want %q", response.Task.PriorWorkDir, "/tmp/legacy-child")
				}
				if response.Task.PriorSessionID != "child-session" {
					t.Errorf("response.Task.PriorSessionID = %q, want %q", response.Task.PriorSessionID, "child-session")
				}
			} else {
				if response.Task.PriorSessionID != "" {
					t.Errorf("unexpected session %q", response.Task.PriorSessionID)
				}
			}
			t.Logf("claim_task=%+v", *response.Task)
		})
	}
}

func TestClaimCodeWorkspace_RejectsInvalidAncestry(t *testing.T) {
	for _, reason := range []string{"cycle", "foreign_workspace", "different_project", "too_deep"} {
		t.Run(reason, func(t *testing.T) {
			// Given
			if testHandler == nil {
				t.Fatal("database is required")
			}
			agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, context.Background())
			rootID := dbfx.Issue(t, "invalid root")
			childID := dbfx.Issue(t, "invalid child", testutil.Cols{"parent_issue_id": rootID})
			switch reason {
			case "cycle":
				dbfx.Exec(t, "UPDATE issue SET parent_issue_id = $1 WHERE id = $2", childID, rootID)
			case "foreign_workspace":
				foreignID := dbfx.Workspace(t, "foreign family", "foreign-code-workspace", testutil.Cols{"issue_prefix": "FRN"})
				dbfx.Exec(t, "UPDATE issue SET workspace_id = $1 WHERE id = $2", foreignID, rootID)
			case "different_project":
				projectID := dbfx.Project(t, "other project")
				dbfx.Exec(t, "UPDATE issue SET project_id = $1 WHERE id = $2", projectID, rootID)
			case "too_deep":
				for range 64 {
					childID = dbfx.Issue(t, "deep child", testutil.Cols{"parent_issue_id": childID})
				}
			}
			taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": childID})
			req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, daemonID)
			req = withURLParam(req, "runtimeId", runtimeID)
			// When
			result := testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusConflict)
			// Then
			var status string
			dbfx.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id = $1", taskID).Scan(&status)
			if status != "queued" {
				t.Errorf("status = %q, want %q", status, "queued")
			}
			t.Logf("claim_response=%s task_status=%s", result.Text(), status)
		})
	}
}

func TestClaimCodeWorkspace_FiltersDirectoryCandidates(t *testing.T) {
	for _, candidate := range []string{"current", "durable", "other_runtime", "old_project", "unknown_snapshot", "missing_directory", "intermediate_issue", "foreign_agent"} {
		t.Run(candidate, func(t *testing.T) {
			// Given
			if testHandler == nil {
				t.Fatal("database is required")
			}
			agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, context.Background())
			projectID := dbfx.Project(t, "current project")
			rootID := dbfx.Issue(t, "checkout root", testutil.Cols{"project_id": projectID})
			parentID := dbfx.Issue(t, "intermediate", testutil.Cols{"project_id": projectID, "parent_issue_id": rootID})
			childID := dbfx.Issue(t, "checkout child", testutil.Cols{"project_id": projectID, "parent_issue_id": parentID})
			cols := testutil.Cols{"runtime_id": runtimeID, "issue_id": rootID, "status": "completed", "work_dir": "/tmp/candidate"}
			candidateAgentID := agentID
			want := ""
			switch candidate {
			case "current":
				want = "/tmp/candidate"
			case "durable":
				cols["work_dir"] = ""
				cols["durable_work_dir"] = "/tmp/candidate"
				want = "/tmp/candidate"
			case "other_runtime":
				cols["runtime_id"] = dbfx.Runtime(t, "other machine")
			case "old_project":
				cols["issue_snapshot"] = `{"v":2,"project_id":"old-project","squad_id":""}`
			case "unknown_snapshot":
				cols["issue_snapshot"] = `{}`
			case "missing_directory":
				cols["work_dir_missing"] = true
			case "intermediate_issue":
				cols["issue_id"] = parentID
			case "foreign_agent":
				foreignID := dbfx.Workspace(t, "foreign candidate", "foreign-workdir-candidate", testutil.Cols{"issue_prefix": "FCW"})
				candidateAgentID = dbfx.Agent(t, "foreign owner", runtimeID, testutil.Cols{"workspace_id": foreignID})
			}
			dbfx.Task(t, candidateAgentID, cols)
			dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": childID})
			req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, daemonID)
			req = withURLParam(req, "runtimeId", runtimeID)
			// When
			var response claimCodeWorkspaceProbe
			testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK).JSON(&response)
			// Then
			if response.Task == nil {
				t.Fatal("claim returned no task")
			}
			if response.Task.CodeWorkspaceIssueID != rootID {
				t.Errorf("root = %q, want %q", response.Task.CodeWorkspaceIssueID, rootID)
			}
			if response.Task.CodeWorkspaceWorkDir != want {
				t.Errorf("candidate = %q, want %q", response.Task.CodeWorkspaceWorkDir, want)
			}
			if response.Task.PriorSessionID != "" {
				t.Errorf("inherited another issue's session: %q", response.Task.PriorSessionID)
			}
			t.Logf("claim_task=%+v", *response.Task)
		})
	}
}

func TestClaimCodeWorkspace_RejectsMissingAncestor(t *testing.T) {
	// Given: the temporary issue table models a dangling legacy parent without
	// changing constraints or rows in the shared database.
	if testHandler == nil {
		t.Fatal("database is required")
	}
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, context.Background())
	rootID := dbfx.Issue(t, "dangling ancestor")
	childID := dbfx.Issue(t, "dangling child", testutil.Cols{"parent_issue_id": rootID})
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": childID})
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), "CREATE TEMP TABLE issue (LIKE public.issue INCLUDING ALL) ON COMMIT DROP"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "INSERT INTO issue SELECT * FROM public.issue WHERE id = $1 OR id = $2", rootID, childID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "UPDATE issue SET parent_issue_id = gen_random_uuid() WHERE id = $1", rootID); err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.Queries = db.New(tx)
	h.TxStarter = tx
	h.TaskService = service.NewTaskService(h.Queries, tx, h.Hub, h.Bus)
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, daemonID)
	req = withURLParam(req, "runtimeId", runtimeID)
	// When
	response := testutil.Call(t, h.ClaimTaskByRuntime, req).Want(http.StatusConflict)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Then
	var status string
	dbfx.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id = $1", taskID).Scan(&status)
	if status != "queued" {
		t.Errorf("status = %q, want queued", status)
	}
	t.Logf("claim_response=%s task_status=%s", response.Text(), status)
}
