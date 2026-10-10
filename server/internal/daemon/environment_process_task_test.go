package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestEnvironmentPrivateHelperProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "owned-environment-private-helper" {
		return
	}
	if err := execenv.RunPreparationHelper(os.Stdin, os.Stdout, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(91)
	}
	os.Exit(0)
}

func TestEnvironmentRunTaskUsesChildSelectionPrivateHelperAndRemoteGitFinish(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	t.Cleanup(cleanup)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/me" {
			if r.Header.Get("Authorization") != "Bearer owned-parent" {
				http.Error(w, "unauthorized", 403)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "owned-account"})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(backend.Close)
	d.client = NewClient(backend.URL)
	d.client.token = "owned-parent"
	d.cfg.ServerBaseURL = backend.URL
	d.cfg.DaemonID = "owned-environment-daemon"
	d.cfg.ProcessServices = []string{"environment"}
	d.cfg.AgentTimeout = 15 * time.Second
	d.cfg.NativeHostBuild = "fixture/commit"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.NativeHostExecutable = executable
	profile, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.NativeVscreenPreferencesPath = filepath.Join(profile, "vscreen.json")
	root, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.WorkspacesRoot = root
	d.localPathLocks = NewLocalPathLocker()
	d.repoCache = &environmentRepoCache{daemon: d}
	d.workspaces["ws-leader"] = &workspaceState{workspaceID: "ws-leader", runtimeIDs: []string{"rt-leader"}, allowedRepoURLs: map[string]struct{}{}}
	d.executionEnvironmentCommand = func() ([]string, error) {
		return []string{executable, "-test.run=^TestEnvironmentPrivateHelperProcess$", "--", "owned-environment-private-helper"}, nil
	}
	t.Logf("REGISTER combined task fixture profile=%s workspace_root=%s fake_provider=%s", profile, root, d.cfg.Agents["claude"].Path)
	t.Cleanup(func() {
		if d.environmentProcess != nil {
			if err := d.environmentProcess.close(); err != nil {
				t.Error(err)
			}
			t.Log("CLEANUP combined environment child and callback closed/reaped")
		}
	})
	script := `#!/bin/sh
set -eu
IFS= read -r _
printf '%s\n' "$MULTICA_TASK_ID" > "$MULTICA_TASK_ID.txt"
printf '{"type":"system","session_id":"owned-session"}\n'
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"owned-session","result":"done"}\n'
`
	writeTestExecutable(t, d.cfg.Agents["claude"].Path, []byte(script))
	repo := createWorktreeTestRepo(t)
	resource, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	first := leaderReuseTestTask("owned-first")
	first.IsLeaderTask = false
	first.DispatchedAt = "2026-10-08T00:00:00Z"
	first.ProjectResources = []ProjectResourceData{{ID: "owned-resource", ResourceType: "local_directory", ResourceRef: resource}}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	result, err := d.runTask(ctx, first, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	if result.WorktreeCommit == "" {
		t.Fatalf("remote Git finish omitted commit: %+v", result)
	}
	before, err := os.Stat(result.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := worktreeTestGit(t, repo, "show", fmt.Sprintf("%s:%s.txt", result.WorktreeCommit, first.ID)); strings.TrimSpace(got) != first.ID {
		t.Fatal("provider edit did not reach service-owned Git commit")
	}
	second := first
	second.ID = "owned-second"
	second.DispatchedAt = "2026-10-08T00:00:01Z"
	second.PriorWorkDir = result.WorkDir
	second.PriorSessionID = result.SessionID
	next, err := d.runTask(ctx, second, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(next.WorkDir)
	if err != nil || !os.SameFile(before, after) || next.CodeRoot == "" || next.EnvRoot == result.EnvRoot {
		t.Fatalf("child did not preserve physical reuse/private roots: %+v %+v %v", result, next, err)
	}
	if next.WorktreeCommit == "" || next.WorktreeCommit == result.WorktreeCommit {
		t.Fatal("second remote finish did not commit its own edit")
	}
	binding, err := execenv.ReadWorktreeBinding(next.CodeRoot)
	if err != nil || len(binding.Consumers) != 0 {
		t.Fatalf("exact consumer release failed: %+v %v", binding, err)
	}
	if _, err = os.Stat(filepath.Join(repo, "owned-first.txt")); !os.IsNotExist(err) {
		t.Fatal("fake provider modified original user checkout")
	}
	inventory, err := d.environmentProcess.process.Client.Read(ctx, "environment.inventory", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var child struct {
		PID int `json:"pid"`
	}
	if err = json.Unmarshal(inventory, &child); err != nil || child.PID == os.Getpid() || child.PID == 0 {
		t.Fatal("physical service was not a child")
	}
	t.Logf("OBSERVE childPID=%d controllerPID=%d roots=%s,%s sharedCode=%s distinctCommits=true consumers=0", child.PID, os.Getpid(), result.EnvRoot, next.EnvRoot, next.CodeRoot)
}
