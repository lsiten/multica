package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedWorktreeDefersCommitAndKeepsEachRunsContext(t *testing.T) {
	params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", TaskID: turnOneTask, AgentName: "J", IssueIdentifier: "LSIT-1", Provider: "claude", IsolateLocalContext: true, LocalWorktree: &LocalWorktreeParams{LocalPath: newTestRepo(t), RetainCheckout: true}, Task: TaskContextForEnv{IssueID: "issue", AgentID: "agent", AgentSkills: []SkillContextForEnv{{Name: "Task skill", Content: "first skill"}}}}
	first, err := Prepare(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer first.ReleaseLock()
	namespace := t.TempDir()
	if err := first.LocalWorktree.BeginSharedExecution(t.Context(), SharedWorktreeDelivery{TaskID: turnOneTask, Namespace: namespace}); err != nil {
		t.Fatal(err)
	}
	if _, err := InjectIsolatedRuntimeConfig(first.ContextDir, "claude", params.Task); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(first.WorkDir, "first.txt"), "unfinished first run")
	reuseParams := LocalWorktreeParams{LocalPath: params.LocalWorktree.LocalPath, EnvRoot: first.RootDir, AgentName: "J", TaskID: turnTwoTask, ConversationKey: "LSIT-1", ConversationID: "issue", WorkspaceID: "ws", AgentID: "agent", RetainCheckout: true, SharedCheckout: true}
	reuseParams.RepositoryScope = first.LocalWorktree.owner.RepositoryScope
	shared, err := ReuseLocalWorktree(first.LocalWorktree, reuseParams, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	runRoot, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: params.WorkspacesRoot, WorkspaceID: "ws", TaskID: turnTwoTask})
	if err != nil {
		t.Fatal(err)
	}
	defer runRoot.Release()
	second := Reuse(ReuseParams{WorkspacesRoot: params.WorkspacesRoot, RunRoot: runRoot.RootDir(), ReusedLocalWorktree: shared, WorkDir: first.WorkDir, Provider: "claude", IsolateLocalContext: true, Task: params.Task}, worktreeTestLogger())
	if second == nil {
		t.Fatal("shared worktree reuse failed")
	}
	if second.ContextDir == first.ContextDir {
		t.Fatal("runs shared their context directory")
	}
	if err := second.LocalWorktree.BeginSharedExecution(t.Context(), SharedWorktreeDelivery{TaskID: turnTwoTask, Namespace: namespace}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(second.WorkDir, "second.txt"), "finished second run")
	if err := CleanupSidecars(second.RootDir); err != nil {
		t.Fatal(err)
	}
	firstSkill := filepath.Join(first.ContextDir, ".claude", "skills", "task-skill", "SKILL.md")
	if got := readFile(t, firstSkill); !strings.Contains(got, "first skill") {
		t.Fatal("second run removed first run's skills")
	}
	pending, err := second.LocalWorktree.Finalize(worktreeTestLogger())
	if err != nil || !pending.DeliveryPending || pending.Branch != "" || pending.Commit != "" {
		t.Fatalf("early run finalized shared files: %+v %v", pending, err)
	}
	if _, err := gitTry(t, first.WorkDir, "show", first.LocalWorktree.Branch+":first.txt"); err == nil {
		t.Fatal("unfinished sibling file was committed")
	}
	writeFile(t, filepath.Join(first.WorkDir, "first.txt"), "finished first run")
	if err := CleanupSidecars(first.RootDir); err != nil {
		t.Fatal(err)
	}
	final, err := first.LocalWorktree.Finalize(worktreeTestLogger())
	if err != nil || final.Commit == "" || final.DeliveryPending {
		t.Fatalf("last run did not settle delivery: %+v %v", final, err)
	}
	for file, want := range map[string]string{"first.txt": "finished first run", "second.txt": "finished second run"} {
		if got := gitRun(t, first.WorkDir, "show", final.Commit+":"+file); got != want {
			t.Fatalf("%s lost: %q", file, got)
		}
	}
	receipts, err := PendingSharedWorktreeDeliveries(t.Context(), namespace)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("delivery receipts not resolved: %+v %v", receipts, err)
	}
	for _, receipt := range receipts {
		if receipt.Commit != final.Commit {
			t.Fatalf("different delivery commits: %+v", receipt)
		}
		if err := AcknowledgeSharedWorktreeDelivery(t.Context(), receipt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrivateProviderCheckoutsDoNotCollideAndRestoreUserConfig(t *testing.T) {
	for _, provider := range []string{"cursor", "omp"} {
		t.Run(provider, func(t *testing.T) {
			code := newTestRepo(t)
			configPath := filepath.Join(code, "."+provider, "mcp.json")
			if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
				t.Fatal(err)
			}
			original := `{"user-owned":true}`
			writeFile(t, configPath, original)
			params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", TaskID: turnOneTask, Provider: provider, AgentName: "J", LocalWorkDir: code, IsolateLocalContext: true, McpConfig: []byte(`{"mcpServers":{"fetch":{"command":"fake-mcp"}}}`)}
			first, err := Prepare(params, worktreeTestLogger())
			if err != nil {
				t.Fatal(err)
			}
			defer first.ReleaseLock()
			params.TaskID = turnTwoTask
			second, err := Prepare(params, worktreeTestLogger())
			if err != nil {
				t.Fatal(err)
			}
			defer second.ReleaseLock()
			if first.WorkDir == code || first.WorkDir == second.WorkDir || first.LocalWorktree.Branch == second.LocalWorktree.Branch {
				t.Fatal("fixed provider configuration was shared")
			}
			if got := readFile(t, configPath); got != original {
				t.Fatalf("user config changed: %q", got)
			}
			for _, env := range []*Environment{first, second} {
				private := filepath.Join(env.WorkDir, "."+provider, "mcp.json")
				if !strings.Contains(readFile(t, private), "fake-mcp") {
					t.Fatal("native provider lost its managed MCP")
				}
				if err := CleanupSidecars(env.RootDir); err != nil {
					t.Fatal(err)
				}
				if got := readFile(t, private); got != original {
					t.Fatalf("private user config not restored: %q", got)
				}
			}
		})
	}
}

func TestIndependentDaemonLeasesKeepGuardUntilLastWriter(t *testing.T) {
	path := t.TempDir()
	first, err := UseSharedDirectory(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := UseSharedDirectory(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Finish(t.Context(), func(last bool) error {
		if last {
			t.Error("first daemon became last prematurely")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, TaskContextMarkerRelPath)); err != nil {
		t.Fatal("first daemon removed shared guard")
	}
	if err := second.Finish(t.Context(), func(last bool) error {
		if !last {
			t.Error("last daemon did not settle")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, TaskContextMarkerRelPath)); !os.IsNotExist(err) {
		t.Fatal("last daemon did not clean shared guard")
	}
}

func TestSharedWorktreeWithoutChangesDoesNotDeliverDeletedBranch(t *testing.T) {
	namespace := t.TempDir()
	env, err := Prepare(PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", TaskID: turnOneTask, Provider: "claude", IsolateLocalContext: true, LocalWorktree: &LocalWorktreeParams{LocalPath: newTestRepo(t)}}, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer env.ReleaseLock()
	if err := env.LocalWorktree.BeginSharedExecution(t.Context(), SharedWorktreeDelivery{TaskID: turnOneTask, Namespace: namespace}); err != nil {
		t.Fatal(err)
	}
	if err := CleanupSidecars(env.RootDir); err != nil {
		t.Fatal(err)
	}
	outcome, err := env.LocalWorktree.Finalize(worktreeTestLogger())
	if err != nil || outcome.Branch != "" || outcome.Commit != "" || outcome.DeliveryPending {
		t.Fatalf("empty worktree delivered a removed branch: %+v %v", outcome, err)
	}
	receipts, err := PendingSharedWorktreeDeliveries(t.Context(), namespace)
	if err != nil || len(receipts) != 1 || !receipts[0].NoWork || receipts[0].Branch != "" || receipts[0].Commit != "" {
		t.Fatalf("empty worktree did not settle without a branch: %+v %v", receipts, err)
	}
	if err := AcknowledgeSharedWorktreeDelivery(t.Context(), receipts[0]); err != nil {
		t.Fatal(err)
	}
}

func TestEmptySettlementPreservesPreviouslyResolvedReceipts(t *testing.T) {
	dir := t.TempDir()
	resolved := SharedWorktreeDelivery{TaskID: turnOneTask, Namespace: "test", Commit: strings.Repeat("a", 40)}
	pending := SharedWorktreeDelivery{TaskID: turnTwoTask, Namespace: "test"}
	for _, receipt := range []SharedWorktreeDelivery{resolved, pending} {
		if err := preserveSharedWorktreeReceipt(dir, receipt); err != nil {
			t.Fatal(err)
		}
	}
	if err := resolveSharedWorktreeReceipts(dir, ""); err != nil {
		t.Fatal(err)
	}
	got, err := readSharedDelivery(sharedDeliveryFile(dir, resolved.TaskID, resolved.Namespace))
	if err != nil || got.Commit != resolved.Commit {
		t.Fatalf("previous delivery was lost: %+v %v", got, err)
	}
	got, err = readSharedDelivery(sharedDeliveryFile(dir, pending.TaskID, pending.Namespace))
	if err != nil || !got.NoWork || got.Branch != "" {
		t.Fatalf("empty delivery did not settle: %+v %v", got, err)
	}
}

func TestPrivateProviderConfigNeverDeletesThroughSymlinkDirectory(t *testing.T) {
	private := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "mcp.json"), "user configuration")
	if err := os.Symlink(outside, filepath.Join(private, ".cursor")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := isolatePrivateProviderFile(private, "cursor", []byte(`{"mcpServers":{}}`), &sidecarManifest{}); err == nil {
		t.Fatal("private config followed an external directory")
	}
	if got := readFile(t, filepath.Join(outside, "mcp.json")); got != "user configuration" {
		t.Fatal("external user config changed")
	}
}

func TestFixedProviderConfigCanUsePrivateNonGitDirectory(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "code.txt"), "source code")
	params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", TaskID: turnOneTask, Provider: "cursor", LocalWorkDir: source, IsolateLocalContext: true, McpConfig: []byte(`{"mcpServers":{"fetch":{"command":"fake-mcp"}}}`)}
	env, err := Prepare(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer env.ReleaseLock()
	if env.WorkDir == source || !env.PrivateProviderCheckout || env.LocalDirectory {
		t.Fatal("fixed configuration was written into shared non-Git directory")
	}
	if got := readFile(t, filepath.Join(env.WorkDir, "code.txt")); got != "source code" {
		t.Fatal("private workspace lost source files")
	}
	if err := CleanupSidecars(env.RootDir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(source, "code.txt")); got != "source code" {
		t.Fatal("source code was changed during private cleanup")
	}
}
