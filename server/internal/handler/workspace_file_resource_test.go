package handler

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceFileIdentitySurvivesRunAndProviderChangesOnOneHost(t *testing.T) {
	host := uuid.NewString()
	firstRuntime := dbfx.Runtime(t, "first file provider", testutil.Cols{"daemon_id": host, "provider": "codex"})
	secondRuntime := dbfx.Runtime(t, "second file provider", testutil.Cols{"daemon_id": host, "provider": "pi"})
	first, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(firstRuntime))
	if err != nil {
		t.Fatal(err)
	}
	second, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(secondRuntime))
	if err != nil {
		t.Fatal(err)
	}
	root := "/files/" + uuid.NewString()
	project, nextProject := dbfx.Project(t, "original file project"), dbfx.Project(t, "next file project")
	owner := dbfx.Issue(t, "persistent file owner", testutil.Cols{"project_id": project})
	firstAgent, secondAgent := dbfx.Agent(t, "first file observer", firstRuntime), dbfx.Agent(t, "second file observer", secondRuntime)
	firstTask := dbfx.Task(t, firstAgent, testutil.Cols{"runtime_id": firstRuntime, "issue_id": owner, "work_dir": root, "status": "completed"})
	secondTask := dbfx.Task(t, secondAgent, testutil.Cols{"runtime_id": secondRuntime, "issue_id": owner, "work_dir": root + "/", "status": "completed"})
	firstScope := newWorkspaceFileDirectory(first, DocumentCommentResource{Kind: "workspace_file", TaskID: firstTask, RuntimeID: firstRuntime, WorkDir: root, OwnerKind: "issue", OwnerID: owner, ProjectID: project})
	secondScope := newWorkspaceFileDirectory(second, DocumentCommentResource{Kind: "workspace_file", TaskID: secondTask, RuntimeID: secondRuntime, WorkDir: root + "/", OwnerKind: "issue", OwnerID: owner, ProjectID: nextProject})
	dbfx.Cleanup(t, "DELETE FROM workspace_file_resource WHERE workspace_id=$1 AND directory_key=$2", testWorkspaceID, firstScope.key)
	initial, err := testHandler.observeWorkspaceFiles(t.Context(), firstScope, []workspaceFileObservation{{Path: "notes.md"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE issue SET project_id=$1 WHERE id=$2", nextProject, owner)

	reopened, err := testHandler.observeWorkspaceFiles(t.Context(), secondScope, []workspaceFileObservation{{Path: "notes.md"}}, true)
	if err != nil {
		t.Fatal(err)
	}

	if initial["notes.md"].ID != reopened["notes.md"].ID {
		t.Fatal("another run/provider replaced the file identity")
	}
	if uuidToString(reopened["notes.md"].ID) == "" {
		t.Fatal("file identity is missing")
	}
	var binding DocumentCommentResource
	if err := json.Unmarshal(reopened["notes.md"].BindingRef, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.TaskID != secondScope.binding.TaskID {
		t.Fatalf("current file route was not refreshed: %+v", binding)
	}
}

func TestWorkspaceFileIdentitySeparatesHostsOwnersResourceScopesAndDirectories(t *testing.T) {
	runtimeID := dbfx.Runtime(t, "isolated file provider", testutil.Cols{"daemon_id": uuid.NewString()})
	runtime, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(runtimeID))
	if err != nil {
		t.Fatal(err)
	}
	root := "/files/" + uuid.NewString()
	base := DocumentCommentResource{Kind: "workspace_file", TaskID: uuid.NewString(), RuntimeID: runtimeID, WorkDir: root, OwnerKind: "issue", OwnerID: uuid.NewString()}
	otherHost, otherOwner, hostedA, hostedB := runtime, runtime, runtime, runtime
	otherHost.DaemonID.String = uuid.NewString()
	otherOwner.OwnerID = parseUUID(uuid.NewString())
	hostedA.DaemonID.Valid, hostedB.DaemonID.Valid = false, false
	hostedB.ID = parseUUID(uuid.NewString())
	cases := []struct {
		runtime db.AgentRuntime
		binding DocumentCommentResource
	}{
		{runtime, base}, {otherHost, base}, {otherOwner, base},
		{runtime, DocumentCommentResource{Kind: "workspace_file", WorkDir: root + "/other", OwnerKind: base.OwnerKind, OwnerID: base.OwnerID}},
		{runtime, DocumentCommentResource{Kind: "workspace_file", WorkDir: root, OwnerKind: "issue", OwnerID: uuid.NewString()}},
		{runtime, DocumentCommentResource{Kind: "workspace_file", WorkDir: root, OwnerKind: "chat", OwnerID: base.OwnerID}},
		{hostedA, base}, {hostedB, base},
	}
	seen := map[string]bool{}
	for _, item := range cases {
		directory := newWorkspaceFileDirectory(item.runtime, item.binding)
		id := directory.key
		if seen[id] {
			t.Fatalf("file identity leaked between resource scopes: %s", id)
		}
		seen[id] = true
	}
}

func TestWorkspaceFilePagesReplaceForgedIdentitiesAndKeepOpaqueMetadata(t *testing.T) {
	runtimeID := dbfx.Runtime(t, "file page provider", testutil.Cols{"daemon_id": uuid.NewString()})
	runtime, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(runtimeID))
	if err != nil {
		t.Fatal(err)
	}
	root := "/fixture/" + uuid.NewString()
	agent := dbfx.Agent(t, "file page observer", runtimeID)
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtimeID, "work_dir": root, "status": "completed"})
	directory := newWorkspaceFileDirectory(runtime, DocumentCommentResource{Kind: "workspace_file", TaskID: task, RuntimeID: runtimeID, WorkDir: root, OwnerKind: "task", OwnerID: task})
	dbfx.Cleanup(t, "DELETE FROM workspace_file_resource WHERE workspace_id=$1 AND directory_key=$2", testWorkspaceID, directory.key)
	forged := uuid.NewString()
	page, err := json.Marshal(map[string]any{"path": directory.binding.WorkDir, "directory_id": "forged", "continuation_hint": "opaque", "entries": []map[string]any{{"path": "notes.md", "directory": false, "file_id": forged, "size": 42}, {"path": ".task_owner", "directory": false, "file_id": forged}}})
	if err != nil {
		t.Fatal(err)
	}
	result := protocol.LocalReviewResult{Page: page}

	if err := testHandler.addWorkspaceFileIdentities(t.Context(), directory, "workspace_files", ".", &result); err != nil {
		t.Fatal(err)
	}

	var response struct {
		DirectoryID string `json:"directory_id"`
		Hint        string `json:"continuation_hint"`
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(result.Page, &response); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result.Page, &decoded); err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Path   string `json:"path"`
		FileID string `json:"file_id"`
		Size   int64  `json:"size"`
	}
	if err := json.Unmarshal(decoded["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	if response.DirectoryID != directory.key || response.Hint != "opaque" || len(entries) != 2 || entries[0].Size != 42 {
		t.Fatalf("page metadata changed: %s", result.Page)
	}
	if _, err := uuid.Parse(entries[0].FileID); err != nil || entries[0].FileID == forged || entries[1].FileID != "" {
		t.Fatalf("runtime supplied a resource identity: %s", result.Page)
	}
	stored, err := testHandler.Queries.GetWorkspaceFileResource(t.Context(), db.GetWorkspaceFileResourceParams{WorkspaceID: runtime.WorkspaceID, DirectoryKey: directory.key, ID: parseUUID(entries[0].FileID)})
	if err != nil || stored.RelativePath != "notes.md" {
		t.Fatalf("file identity has no authoritative location: %+v, %v", stored, err)
	}
}
