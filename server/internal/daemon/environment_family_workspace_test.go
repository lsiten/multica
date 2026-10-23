package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestFamilyWorkspaceReusesAcrossAgentsAndDescendants(t *testing.T) {
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	first := leaderReuseTestTask("family-first")
	first.CodeWorkspaceIssueID = "root-issue"
	first.CodeWorkspaceIssueIdentifier = "ROOT-1"
	result, err := d.runTask(context.Background(), first, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, filepath.Join(result.WorkDir, "family-code.txt"), "shared edits")
	for _, id := range []string{"child", "grandchild"} {
		next := first
		next.ID, next.IssueID, next.AgentID = "family-"+id, id, "agent-"+id
		agent := *first.Agent
		agent.ID = next.AgentID
		next.Agent = &agent
		next.PriorWorkDir = ""
		resumed, err := d.runTask(context.Background(), next, "claude", 0, d.logger)
		if err != nil {
			t.Fatal(err)
		}
		if !sameDir(t, resumed.WorkDir, result.WorkDir) {
			t.Fatalf("%s has independent code directory: %s != %s", id, resumed.WorkDir, result.WorkDir)
		}
		if sameDir(t, resumed.EnvRoot, result.EnvRoot) {
			t.Fatal("run configurations share their root")
		}
		if data, err := os.ReadFile(filepath.Join(resumed.WorkDir, "family-code.txt")); err != nil || string(data) != "shared edits" {
			t.Fatalf("shared edits missing: %q %v", data, err)
		}
	}
}

func TestFamilyWorkspaceLocalCheckoutAdoptsRootAndPreservesOldChild(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			d.localPathLocks = NewLocalPathLocker()
			d.cfg.DaemonID = "family-daemon"
			repo := createWorktreeTestRepo(t)
			ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
			if err != nil {
				t.Fatal(err)
			}
			root := leaderReuseTestTask("root-run")
			root.IsLeaderTask = false
			root.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
			if !legacy {
				root.CodeWorkspaceIssueID = root.IssueID
				root.CodeWorkspaceIssueIdentifier = "ROOT-1"
			}
			first, err := d.runTask(t.Context(), root, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			writeLifecycleFile(t, filepath.Join(first.WorkDir, "root-code.txt"), "root code")
			worktreeTestGit(t, first.WorkDir, "add", "root-code.txt")
			worktreeTestGit(t, first.WorkDir, "commit", "-m", "root code")
			child := root
			child.ID = "old-child-run"
			child.IssueID = "child"
			child.CodeWorkspaceIssueID = ""
			child.CodeWorkspaceIssueIdentifier = ""
			old, err := d.runTask(t.Context(), child, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			writeLifecycleFile(t, filepath.Join(old.WorkDir, "unfinished.txt"), "unfinished child work")
			child.ID = "new-child-run"
			child.AgentID = "child-agent"
			agent := *child.Agent
			agent.ID = child.AgentID
			agent.Name = "different-agent"
			child.Agent = &agent
			child.CodeWorkspaceIssueID = root.IssueID
			child.CodeWorkspaceIssueIdentifier = "ROOT-1"
			child.CodeWorkspaceWorkDir = first.WorkDir
			child.PriorWorkDir = old.WorkDir
			child.PriorSessionID = "old-session"
			next, err := d.runTask(t.Context(), child, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			if !sameDir(t, next.WorkDir, first.WorkDir) {
				t.Fatalf("child failed to share root: %s != %s", next.WorkDir, first.WorkDir)
			}
			if got, err := os.ReadFile(filepath.Join(old.WorkDir, "unfinished.txt")); err != nil || string(got) != "unfinished child work" {
				t.Fatalf("old child work lost: %q %v", got, err)
			}
			if got, err := os.ReadFile(filepath.Join(next.WorkDir, "root-code.txt")); err != nil || string(got) != "root code" {
				t.Fatalf("root work lost: %q %v", got, err)
			}
			grandchild := child
			grandchild.ID = "grandchild-run"
			grandchild.IssueID = "grandchild"
			grandchild.CodeWorkspaceWorkDir = ""
			grandchild.PriorWorkDir = ""
			grandchild.PriorSessionID = ""
			last, err := d.runTask(t.Context(), grandchild, "claude", 0, d.logger)
			if err != nil || !sameDir(t, last.WorkDir, first.WorkDir) {
				t.Fatalf("grandchild did not discover adopted family root: %+v %v", last, err)
			}
		})
	}
}

func TestFamilyWorkspaceConcurrentFirstRunsShareCode(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			d.localPathLocks = NewLocalPathLocker()
			d.cfg.DaemonID = "family-daemon"
			first := leaderReuseTestTask("concurrent-first")
			first.IsLeaderTask = false
			first.CodeWorkspaceIssueID = "root"
			if local {
				repo := createWorktreeTestRepo(t)
				ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
				if err != nil {
					t.Fatal(err)
				}
				first.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
			}
			second := first
			second.ID = "concurrent-second"
			second.IssueID = "sibling"
			second.AgentID = "sibling-agent"
			agent := *first.Agent
			agent.ID = second.AgentID
			agent.Name = "sibling-agent"
			second.Agent = &agent
			type outcome struct {
				result TaskResult
				err    error
			}
			done := make(chan outcome, 2)
			start := make(chan struct{})
			for _, task := range []Task{first, second} {
				go func() {
					<-start
					result, err := d.runTask(t.Context(), task, "claude", 0, d.logger)
					done <- outcome{result, err}
				}()
			}
			close(start)
			a, b := <-done, <-done
			if a.err != nil || b.err != nil {
				t.Fatalf("concurrent runs failed: %v %v", a.err, b.err)
			}
			if !sameDir(t, a.result.WorkDir, b.result.WorkDir) {
				t.Fatalf("first runs forked code directories: %s != %s", a.result.WorkDir, b.result.WorkDir)
			}
			if sameDir(t, a.result.EnvRoot, b.result.EnvRoot) {
				t.Fatal("run config directories were shared")
			}
		})
	}
}

func TestFamilyWorkspaceRejectsOtherExecutionScopes(t *testing.T) {
	task := leaderReuseTestTask("family")
	task.CodeWorkspaceIssueID = "root"
	task.ProjectID = "project"
	scope, err := managedScopeForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	scope.ManagedBy = execenv.ManagedEnvProvenanceManagedBy
	for _, change := range []string{"same", "agent", "squad", "root", "workspace", "runtime", "project", "repository", "private-provider", "legacy-child"} {
		t.Run(change, func(t *testing.T) {
			candidate := task
			provenance := scope
			switch change {
			case "agent":
				candidate.AgentID = "other"
			case "squad":
				candidate.SquadID = "other"
			case "root":
				candidate.CodeWorkspaceIssueID = "other"
			case "workspace":
				candidate.WorkspaceID = "other"
			case "runtime":
				candidate.RuntimeID = "other"
			case "project":
				candidate.ProjectID = "other"
			case "repository":
				candidate.Repos = []RepoData{{URL: "https://example.test/other"}}
			case "private-provider":
				provenance.PrivateProviderCheckout = true
			case "legacy-child":
				provenance.CodeWorkspaceIssueID = ""
			}
			want := change == "same" || change == "agent" || change == "squad"
			if got := managedReuseScopeMatches(candidate, &provenance); got != want {
				t.Fatalf("reuse=%v want=%v", got, want)
			}
		})
	}
}

func TestFamilyWorkspaceCodeRootRetentionIncludesUnfinishedFamily(t *testing.T) {
	for _, scenario := range []string{"root-open", "family-done", "old-server", "wrong-root", "run-config", "legacy-agent"} {
		t.Run(scenario, func(t *testing.T) {
			var workdir string
			status := protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", LifecycleSupported: true, RetentionSupported: true, IssueID: "child", IssueStatus: "done", IssueStatusCategory: "done", CodeWorkspaceIssueID: "root", CodeWorkspaceRetentionBlocked: true}
			switch scenario {
			case "family-done":
				status.CodeWorkspaceRetentionBlocked = false
			case "old-server":
				status.CodeWorkspaceIssueID = ""
			case "wrong-root":
				status.CodeWorkspaceIssueID = "other"
			}
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status.WorkDir = workdir
				status.CurrentWorkDir = workdir
				json.NewEncoder(w).Encode(status)
			}))
			d.cfg.GCEnabled, d.cfg.EnvironmentRecycleEnabled, d.cfg.EnvironmentArchiveTTL = true, true, 24*time.Hour
			meta := &execenv.GCMeta{Kind: execenv.GCKindIssue, IssueID: "child", TaskID: "task", WorkspaceID: "ws1", CompletedAt: time.Now()}
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", meta)
			workdir = filepath.Join(root, "workdir")
			if scenario != "run-config" {
				writeLifecycleFile(t, filepath.Join(workdir, "code.txt"), "family code")
			}
			scope := execenv.ManagedEnvProvenance{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", IssueID: "child", CodeWorkspaceIssueID: "root"}
			if scenario == "legacy-agent" {
				scope.CodeWorkspaceIssueID = ""
				scope.AgentID = "retired-agent"
			}
			if err := execenv.WriteManagedEnvProvenance(root, scope); err != nil {
				t.Fatal(err)
			}
			eligible, reason := d.automaticCleanupEligible(t.Context(), root, meta)
			want := scenario == "family-done" || scenario == "run-config"
			if eligible != want {
				t.Fatalf("cleanup=%v want=%v reason=%s", eligible, want, reason)
			}
		})
	}
}

func TestFamilyWorkspaceChangedDirectoryDropsProviderResume(t *testing.T) {
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	for _, provider := range []string{"claude", "hermes", "pi"} {
		t.Run(provider, func(t *testing.T) {
			task := leaderReuseTestTask("next")
			task.CodeWorkspaceIssueID = "root"
			task.PriorWorkDir = t.TempDir()
			task.PriorSessionID = "previous-session"
			ctx := execenv.TaskContextForEnv{PriorSessionResumed: true}
			gateResumeToReachableSession(&task, &ctx, provider, t.TempDir(), true, false, d.logger)
			if task.PriorSessionID != "" || ctx.PriorSessionResumed {
				t.Fatal("moved issue retained its previous provider session")
			}
		})
	}
}

func TestFamilyWorkspacePhysicalServiceSkipsReservedRunRoots(t *testing.T) {
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	d.environmentServiceOwner = true
	d.workspaces["ws-leader"] = &workspaceState{runtimeIDs: []string{"rt-leader"}}
	owner, err := newEnvironmentPhysicalOwner(d.cfg.WorkspacesRoot, t.TempDir(), d.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	d.cfg.WorkspacesRoot = owner.root
	service := &environmentProcessService{daemon: d, physical: owner}
	var first string
	for _, id := range []string{"first", "sibling"} {
		task := leaderReuseTestTask(id)
		task.CodeWorkspaceIssueID = "root"
		task.IssueID = id
		input, err := physicalInputForTask(task, "fake")
		if err != nil {
			t.Fatal(err)
		}
		input.ReserveOnly = true
		reserved, err := owner.prepare(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		claim, err := execenv.ClaimPhysicalRoot(d.cfg.WorkspacesRoot, reserved.Reservation)
		if err != nil {
			t.Fatal(err)
		}
		defer claim.Release()
		if err := owner.claimRoot(reserved.Reservation); err != nil {
			t.Fatal(err)
		}
		selected, err := service.selectPhysical(t.Context(), environmentPhysicalSelection{PreparationID: reserved.Reservation.ID, Physical: input, Consumer: execenv.WorktreeConsumer{TaskID: task.ID, AgentID: task.AgentID, RuntimeID: task.RuntimeID}})
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = selected.WorkDir
			writeLifecycleFile(t, filepath.Join(first, "retained.txt"), "code")
		} else {
			if !sameDir(t, first, selected.WorkDir) {
				t.Fatalf("reserved empty run root displaced existing family checkout: first=%s selected=%+v discovered=%s", first, selected, d.discoverManagedWorkdir(t.Context(), physicalTaskForSelection(environmentPhysicalSelection{Physical: input})))
			}
			if code, err := environmentHasCode(selected.Reservation.RootDir); err != nil || code {
				t.Fatalf("private run root classified as shared code: %v %v", code, err)
			}
		}
	}
}

func TestFamilyWorkspacePreservesMigratedChildThroughCleanup(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			d.localPathLocks = NewLocalPathLocker()
			d.cfg.DaemonID = "family-daemon"
			task := leaderReuseTestTask("old-child")
			task.IsLeaderTask = false
			task.IssueID = "child"
			if local {
				repo := createWorktreeTestRepo(t)
				ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
				if err != nil {
					t.Fatal(err)
				}
				task.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
			}
			old, err := d.runTask(t.Context(), task, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			writeLifecycleFile(t, filepath.Join(old.WorkDir, "unfinished.txt"), "unpublished")
			next := task
			next.ID = "migrated-child"
			next.CodeWorkspaceIssueID = "root"
			next.PriorWorkDir = old.WorkDir
			result, err := d.runTask(t.Context(), next, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			if sameDir(t, old.WorkDir, result.WorkDir) {
				t.Fatal("old child remained canonical")
			}
			scope := protocol.TaskGCStatus{WorkspaceID: task.WorkspaceID, RuntimeID: task.RuntimeID, AgentID: task.AgentID, Status: "completed", LifecycleSupported: true, RetentionSupported: true, IssueID: "child", IssueStatus: "done", IssueStatusCategory: "done", CurrentWorkDir: result.WorkDir}
			if reason := d.environmentRetentionReason(t.Context(), old.EnvRoot, &scope); reason != "retained" {
				t.Fatalf("old child exposed to cleanup: %s", reason)
			}
			if got, err := os.ReadFile(filepath.Join(old.WorkDir, "unfinished.txt")); err != nil || string(got) != "unpublished" {
				t.Fatalf("old work lost: %q %v", got, err)
			}
		})
	}
}

func TestFamilyWorkspaceRestoresArchivedCodeAcrossAgents(t *testing.T) {
	for _, mode := range []string{"new-no-hint", "new-other-scope-hint", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			legacy := mode == "legacy"
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: "completed", WorkspaceID: "ws-leader", RuntimeID: "rt-leader", AgentID: "agent-leader", LifecycleSupported: true, RetentionSupported: true, CodeWorkspaceIssueID: "root"})
			}))
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws-leader", "first", &execenv.GCMeta{WorkspaceID: "ws-leader", TaskID: "first"})
			workdir := filepath.Join(root, "workdir")
			writeLifecycleFile(t, filepath.Join(workdir, "retained.txt"), "archived family code")
			task := leaderReuseTestTask("second")
			task.CodeWorkspaceIssueID = "root"
			scope, err := managedScopeForTask(task)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				scope.CodeWorkspaceIssueID = ""
				scope.IssueID = "root"
				task.CodeWorkspaceWorkDir = workdir
				writeLeaderTaskMarker(t, workdir, "agent-leader", "root")
			}
			if err := execenv.WriteManagedEnvProvenance(root, scope); err != nil {
				t.Fatal(err)
			}
			preview := d.archiveEnvironmentOperation(t.Context(), root, "", "")
			archived := d.archiveEnvironmentOperation(t.Context(), root, preview.Revision, strings.Repeat("b", 64))
			if !archived.Reclaimed {
				t.Fatalf("archive failed: %+v", archived)
			}
			if mode == "new-other-scope-hint" {
				other := createTaskDir(t, d.cfg.WorkspacesRoot, "ws-leader", "other-scope", &execenv.GCMeta{WorkspaceID: "ws-leader", TaskID: "other-scope"})
				hint := filepath.Join(other, "workdir")
				writeLifecycleFile(t, filepath.Join(hint, "different.txt"), "different repository")
				otherScope := scope
				otherScope.RepositoryScope = strings.Repeat("f", 64)
				if err := execenv.WriteManagedEnvProvenance(other, otherScope); err != nil {
					t.Fatal(err)
				}
				task.CodeWorkspaceWorkDir = hint
			}
			task.AgentID = "other-agent"
			task.IssueID = "grandchild"
			claim, path, _, ok, err := d.lockReusablePriorEnvRoot(t.Context(), task, nil, "")
			if claim != nil {
				defer claim.Release()
			}
			if err != nil || !ok || !sameDir(t, path, workdir) {
				t.Fatalf("family archive not restored: path=%s ok=%v err=%v", path, ok, err)
			}
			if got, err := os.ReadFile(filepath.Join(path, "retained.txt")); err != nil || string(got) != "archived family code" {
				t.Fatalf("archived code lost: %q %v", got, err)
			}
		})
	}
}

func TestFamilyWorkspaceFirstRunsAcrossProcesses(t *testing.T) {
	if root := os.Getenv("MULTICA_TEST_FAMILY_RUN_ROOT"); root != "" {
		d, _, cleanup := newLeaderReuseTestDaemon(t)
		defer cleanup()
		d.cfg.WorkspacesRoot = root
		d.cfg.DaemonID = "family-daemon"
		d.localPathLocks = NewLocalPathLocker()
		id := os.Getenv("MULTICA_TEST_FAMILY_RUN_ID")
		task := leaderReuseTestTask(id)
		task.CodeWorkspaceIssueID = "root"
		task.IssueID = id
		task.AgentID = id + "-agent"
		task.Agent.ID = task.AgentID
		task.Agent.Name = task.AgentID
		task.IsLeaderTask = false
		if repo := os.Getenv("MULTICA_TEST_FAMILY_REPO"); repo != "" {
			ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
			if err != nil {
				t.Fatal(err)
			}
			task.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
		}
		result, err := d.runTask(t.Context(), task, "claude", 0, d.logger)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("MULTICA_TEST_FAMILY_RESULT"), data, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			root := t.TempDir()
			repo := ""
			if local {
				repo = createWorktreeTestRepo(t)
			}
			commands := make([]*exec.Cmd, 2)
			outputs := make([]bytes.Buffer, 2)
			paths := make([]string, 2)
			for i := range commands {
				paths[i] = filepath.Join(t.TempDir(), "result.json")
				command := exec.CommandContext(t.Context(), executable, "-test.run=^TestFamilyWorkspaceFirstRunsAcrossProcesses$", "-test.timeout=40s")
				command.Env = append(os.Environ(), "MULTICA_TEST_FAMILY_RUN_ROOT="+root, fmt.Sprintf("MULTICA_TEST_FAMILY_RUN_ID=child-%d", i), "MULTICA_TEST_FAMILY_RESULT="+paths[i], "MULTICA_TEST_FAMILY_REPO="+repo)
				command.Stdout = &outputs[i]
				command.Stderr = &outputs[i]
				commands[i] = command
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
			}
			results := make([]TaskResult, 2)
			for i, command := range commands {
				if err := command.Wait(); err != nil {
					t.Errorf("worker %d failed: %v\n%s", i, err, outputs[i].String())
					continue
				}
				data, err := os.ReadFile(paths[i])
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &results[i]); err != nil {
					t.Fatal(err)
				}
			}
			if t.Failed() {
				return
			}
			if !sameDir(t, results[0].WorkDir, results[1].WorkDir) {
				t.Fatalf("processes created separate code directories: %s != %s", results[0].WorkDir, results[1].WorkDir)
			}
			if sameDir(t, results[0].EnvRoot, results[1].EnvRoot) {
				t.Fatal("processes share run configuration root")
			}
		})
	}
}

func TestFamilyWorkspaceOwnerTaskRedeliveryPreservesDescendantCode(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			d.localPathLocks = NewLocalPathLocker()
			d.cfg.DaemonID = "family-daemon"
			task := leaderReuseTestTask("owner-run")
			task.CodeWorkspaceIssueID = "root"
			task.IsLeaderTask = false
			if local {
				repo := createWorktreeTestRepo(t)
				ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
				if err != nil {
					t.Fatal(err)
				}
				task.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
			}
			first, err := d.runTask(t.Context(), task, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			child := task
			child.ID = "child-run"
			child.IssueID = "child"
			if _, err := d.runTask(t.Context(), child, "claude", 0, d.logger); err != nil {
				t.Fatal(err)
			}
			writeLifecycleFile(t, filepath.Join(first.WorkDir, "child-unfinished.txt"), "retain descendant edits")
			next, err := d.runTask(t.Context(), task, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			if !sameDir(t, first.WorkDir, next.WorkDir) {
				t.Fatal("redelivery replaced family checkout")
			}
			if got, err := os.ReadFile(filepath.Join(next.WorkDir, "child-unfinished.txt")); err != nil || string(got) != "retain descendant edits" {
				t.Fatalf("redelivery lost descendant code: %q %v", got, err)
			}
		})
	}
}

func TestFamilyWorkspaceOwnerRedeliveryRejectsChangedScopeWithoutReset(t *testing.T) {
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	task := leaderReuseTestTask("owner")
	task.CodeWorkspaceIssueID = "root"
	first, err := d.runTask(t.Context(), task, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, filepath.Join(first.WorkDir, "preserve.txt"), "original family")
	task.CodeWorkspaceIssueID = "different-root"
	if _, err := d.runTask(t.Context(), task, "claude", 0, d.logger); err == nil {
		t.Fatal("redelivered owner changed family scope")
	}
	if got, err := os.ReadFile(filepath.Join(first.WorkDir, "preserve.txt")); err != nil || string(got) != "original family" {
		t.Fatalf("scope mismatch reset code: %q %v", got, err)
	}
}
