package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestEnvironmentMutationScopeConflicts(t *testing.T) {
	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	scopeRequireNoError(t, os.MkdirAll(filepath.Join(left, "child"), 0700))
	scopeRequireNoError(t, os.Mkdir(right, 0700))
	alias := filepath.Join(root, "alias")
	scopeRequireNoError(t, os.Symlink(left, alias))
	makeScope := func(workspace, path string) environmentMutationScope {
		result := environmentMutationScope{WorkspaceID: workspace}
		result.addPath(path)
		return result
	}
	cases := []struct {
		name        string
		left, right environmentMutationScope
		conflict    bool
	}{
		{"independent same workspace directories", makeScope("ws", left), makeScope("ws", right), false},
		{"cross workspace symlink aliases", makeScope("ws-a", left), makeScope("ws-b", alias), true},
		{"parent child paths", makeScope("ws-a", left), makeScope("ws-b", filepath.Join(alias, "child")), true},
		{"workspace maintenance", environmentMutationScope{WorkspaceID: "ws", WorkspaceWide: true}, makeScope("ws", right), true},
		{"other workspace maintenance", environmentMutationScope{WorkspaceID: "other", WorkspaceWide: true}, makeScope("ws", right), false},
		{"missing workspace identity", environmentMutationScope{WorkspaceWide: true}, makeScope("ws", right), true},
		{"global maintenance", environmentMutationScope{Global: true}, makeScope("ws", right), true},
		{"unresolved path", makeScope("ws", filepath.Join(root, "missing")), makeScope("other", right), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scopeRequire(t, tc.conflict == tc.left.conflicts(tc.right))
			scopeRequire(t, tc.conflict == tc.right.conflicts(tc.left))
		})
	}
}

func TestEnvironmentMutationScopePreparationUsesResources(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	unrelated := filepath.Join(root, "unrelated")
	scopeRequireNoError(t, os.Mkdir(shared, 0700))
	scopeRequireNoError(t, os.Mkdir(unrelated, 0700))
	owner := &environmentPhysicalOwner{preparations: map[string]*environmentPhysicalPreparation{}}
	service := &environmentProcessService{physical: owner}
	for id, path := range map[string]string{"first": shared, "second": shared, "independent": unrelated} {
		owner.preparations[id] = &environmentPhysicalPreparation{Result: environmentPhysicalResult{
			Reservation: execenv.PhysicalRootReservation{ID: id, WorkspaceID: "ws", TaskID: id, RootDir: path}, CodeRoot: path, WorkDir: path,
		}}
	}
	first := service.preparationMutationScope("first")
	scopeRequire(t, first.conflicts(service.preparationMutationScope("second")))
	scopeRequire(t, !first.conflicts(service.preparationMutationScope("independent")))
	scopeRequire(t, first.conflicts(service.preparationMutationScope("unknown")))
	t.Log("distinct preparation IDs and task IDs sharing CodeRoot conflict; independent CodeRoot remains available; unknown preparation fails closed")
}

func TestEnvironmentMutationScopeFamilyIgnoresChildAndAgent(t *testing.T) {
	service := &environmentProcessService{}
	input := execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task-a", AgentID: "agent-a", IssueID: "child-a", CodeWorkspaceIssueID: "family", RepositoryScope: strings.Repeat("a", 64)}
	first := service.prepareMutationScope(t.Context(), input)
	input.TaskID, input.IssueID, input.AgentID, input.SquadID = "task-b", "child-b", "agent-b", "squad-b"
	second := service.prepareMutationScope(t.Context(), input)
	scopeRequire(t, !first.Global)
	scopeRequire(t, !first.WorkspaceWide)
	scopeRequire(t, first.conflicts(second))
	input.CodeWorkspaceIssueID = "unrelated"
	scopeRequire(t, !first.conflicts(service.prepareMutationScope(t.Context(), input)))
}

func TestEnvironmentMutationScopeReadRejectsMalformedAndUnknown(t *testing.T) {
	service := &environmentProcessService{}
	for _, raw := range []string{`{`, `{"operation":"physical.abort","payload":null}`, `{"operation":"unknown","payload":{}}`, `{"operation":"physical.abort","payload":{"preparation_id":42}}`, `{"operation":"environment.gc","payload":{"unexpected":true}}`} {
		_, err := service.mutationScopeRead(t.Context(), runtimeproc.Request{Payload: json.RawMessage(raw)})
		scopeRequire(t, err != nil)
	}
	for _, operation := range []string{"environment.bind", "environment.gc"} {
		payload, err := json.Marshal(struct {
			Operation string   `json:"operation"`
			Payload   struct{} `json:"payload"`
		}{Operation: operation})
		scopeRequireNoError(t, err)
		raw, rpcErr := service.mutationScopeRead(t.Context(), runtimeproc.Request{Payload: payload})
		scopeRequire(t, rpcErr == nil)
		var scope environmentMutationScope
		scopeRequireNoError(t, json.Unmarshal(raw, &scope))
		scopeRequire(t, scope.Global)
	}
}

func TestEnvironmentMutationScopeSelectionResolvesOpaqueOwnership(t *testing.T) {
	daemon := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir()}}
	root := createTaskDir(t, daemon.cfg.WorkspacesRoot, "ws", "task", nil)
	owner, err := daemon.gcTaskDirOwner(root)
	scopeRequireNoError(t, err)
	service := &environmentProcessService{daemon: daemon}
	input := environmentOperationInput{WorkspaceID: "ws", Action: "clean_cache", Selection: protocol.EnvironmentSelection{EnvironmentID: daemon.managedEnvironmentID(root, "ws", owner.TaskID)}}
	scope := service.operationMutationScope(t.Context(), input)
	scopeRequire(t, !scope.Global)
	scopeRequire(t, !scope.WorkspaceWide)
	scopeRequire(t, len(scope.Paths) > 0)
	input.WorkspaceID = "other"
	scopeRequire(t, service.operationMutationScope(t.Context(), input).WorkspaceWide)
	input.WorkspaceID = "ws"
	input.Selection.EnvironmentID = root
	scopeRequire(t, service.operationMutationScope(t.Context(), input).WorkspaceWide)
	t.Log("only authorized opaque environment ID resolves; another workspace and raw path cannot narrow the scope")
}

func TestEnvironmentMutationScopeLimitBroadens(t *testing.T) {
	scope := environmentMutationScope{WorkspaceID: "ws"}
	for index := range 17 {
		scope.addKey(strings.Repeat("a", index+1))
	}
	scopeRequire(t, scope.Global)
	scopeRequire(t, len(scope.Keys) <= 16)
}

func scopeRequire(t *testing.T, condition bool) {
	t.Helper()
	if !condition {
		t.Fatal("scope invariant failed")
	}
}
func scopeRequireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentMutationScopeGitSiblingsShareBoundary(t *testing.T) {
	repo := createWorktreeTestRepo(t)
	firstPath, secondPath := filepath.Join(repo, "first"), filepath.Join(repo, "second")
	scopeRequireNoError(t, os.Mkdir(firstPath, 0700))
	scopeRequireNoError(t, os.Mkdir(secondPath, 0700))
	first := environmentMutationScope{WorkspaceID: "first-workspace"}
	second := environmentMutationScope{WorkspaceID: "second-workspace"}
	first.addPath(firstPath)
	second.addPath(secondPath)
	scopeRequire(t, !first.conflicts(second))
	first.addGitBoundaries(t.Context())
	second.addGitBoundaries(t.Context())
	scopeRequire(t, !first.Global && !second.Global)
	scopeRequire(t, first.conflicts(second))
	t.Log("sibling directories with independent paths conflict through actual shared Git repository metadata")
}

func TestEnvironmentMutationScopeDiscoversFamilyCheckout(t *testing.T) {
	daemon := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir()}}
	root := createTaskDir(t, daemon.cfg.WorkspacesRoot, "ws", "old-task", nil)
	scopeRequireNoError(t, execenv.WriteManagedEnvProvenance(root, execenv.ManagedEnvProvenance{WorkspaceID: "ws", CodeWorkspaceIssueID: "family", IssueID: "old-child", AgentID: "old-agent", RepositoryScope: strings.Repeat("a", 64)}))
	service := &environmentProcessService{daemon: daemon}
	input := execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "new-task", IssueID: "new-child", AgentID: "new-agent", CodeWorkspaceIssueID: "family", RepositoryScope: strings.Repeat("a", 64)}
	scope := service.prepareMutationScope(t.Context(), input)
	expected := environmentMutationScope{WorkspaceID: "ws"}
	expected.addPath(root)
	scopeRequire(t, !scope.Global && !scope.WorkspaceWide)
	scopeRequire(t, scope.conflicts(expected))
	t.Log("new family task discovers the historical family's actual checkout root")
}

func TestEnvironmentMutationScopeLinkedWorktreesShareMetadata(t *testing.T) {
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(t.TempDir(), "linked")
	_, err := worktreeGit(t.Context(), repo, "worktree", "add", "--detach", checkout, "HEAD")
	scopeRequireNoError(t, err)
	first := environmentMutationScope{WorkspaceID: "first-workspace"}
	second := environmentMutationScope{WorkspaceID: "second-workspace"}
	first.addPath(repo)
	second.addPath(checkout)
	scopeRequire(t, !first.conflicts(second))
	first.addGitBoundaries(t.Context())
	second.addGitBoundaries(t.Context())
	scopeRequire(t, !first.Global && !second.Global)
	scopeRequire(t, first.conflicts(second))
	t.Log("cross-workspace linked checkouts conflict through canonical git-common-dir")
}
