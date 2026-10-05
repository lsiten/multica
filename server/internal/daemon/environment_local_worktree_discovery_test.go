package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunTaskFindsSharedLocalBranchWithoutUsablePriorWorkdir(t *testing.T) {
	for _, prior := range []string{"missing", "stale", "different_issue"} {
		t.Run(prior, func(t *testing.T) {
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			d.localPathLocks = NewLocalPathLocker()
			d.cfg.DaemonID = "shared-branch-daemon"
			repo := createWorktreeTestRepo(t)
			ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
			if err != nil {
				t.Fatal(err)
			}
			first := leaderReuseTestTask("task-first")
			first.IsLeaderTask = false
			first.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
			result, err := d.runTask(t.Context(), first, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			writeLifecycleFile(t, filepath.Join(result.WorkDir, "shared.txt"), "prior branch delivery")
			worktreeTestGit(t, result.WorkDir, "add", "shared.txt")
			worktreeTestGit(t, result.WorkDir, "commit", "-m", "prior branch delivery")
			branch := worktreeTestGit(t, result.WorkDir, "symbolic-ref", "--short", "HEAD")
			before, err := os.Stat(result.WorkDir)
			if err != nil {
				t.Fatal(err)
			}
			second := first
			second.ID = "task-second"
			switch prior {
			case "stale":
				second.PriorWorkDir = filepath.Join(d.cfg.WorkspacesRoot, "missing", "previous", "worktree")
			case "different_issue":
				second.IssueID = "other-issue"
				second.PriorWorkDir = result.WorkDir
			}
			next, err := d.runTask(t.Context(), second, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(next.WorkDir)
			if err != nil {
				t.Fatal(err)
			}
			nextBranch := worktreeTestGit(t, next.WorkDir, "symbolic-ref", "--short", "HEAD")
			if prior == "different_issue" {
				if os.SameFile(before, after) || nextBranch == branch {
					t.Fatal("another issue reused the shared branch or checkout")
				}
				if _, err := os.Stat(filepath.Join(next.WorkDir, "shared.txt")); !os.IsNotExist(err) {
					t.Fatal("another issue inherited the shared branch's delivery")
				}
				return
			}
			if !os.SameFile(before, after) || nextBranch != branch {
				t.Fatalf("%s prior directory forked instead of reusing branch %s: %+v", prior, branch, next)
			}
			if got, err := os.ReadFile(filepath.Join(next.WorkDir, "shared.txt")); err != nil || string(got) != "prior branch delivery" {
				t.Fatalf("shared branch delivery lost: %q %v", got, err)
			}
			if sameDir(t, result.EnvRoot, next.EnvRoot) || next.CodeRoot == "" {
				t.Fatal("shared branch reuse lost per-run configuration isolation")
			}
		})
	}
}

func TestConcurrentRunsShareLocalWorktreeAndBranch(t *testing.T) {
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	d.localPathLocks = NewLocalPathLocker()
	d.cfg.DaemonID = "concurrent-worktree-daemon"
	d.cfg.AgentTimeout = 10 * time.Second
	signals := t.TempDir()
	quotedSignals := "'" + strings.ReplaceAll(signals, "'", "'\\''") + "'"
	script := fmt.Sprintf(`#!/bin/sh
IFS= read -r _
printf '{"type":"system","session_id":"session-%%s"}\n' "$MULTICA_TASK_ID"
signals=%s
pwd > "$signals/$MULTICA_TASK_ID.cwd"
printf '%%s' "$MULTICA_TASK_CONFIG_ROOT" > "$signals/$MULTICA_TASK_ID.config"
printf '%%s' "$MULTICA_TASK_ID" > "$MULTICA_TASK_ID.txt"
if [ "$MULTICA_TASK_ID" = "task-first" ]; then
  : > "$signals/started"
  count=0
  while [ ! -f "$signals/release" ]; do
    count=$((count + 1))
    [ "$count" -lt 400 ] || exit 1
    sleep 0.02
  done
fi
printf '{"type":"result","subtype":"success","is_error":false,"session_id":"session-%%s","result":"done"}\n' "$MULTICA_TASK_ID"
`, quotedSignals)
	writeTestExecutable(t, d.cfg.Agents["claude"].Path, []byte(script))
	repo := createWorktreeTestRepo(t)
	ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	first := leaderReuseTestTask("task-first")
	first.IsLeaderTask = false
	first.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
	type runOutcome struct {
		result TaskResult
		err    error
	}
	finished := make(chan runOutcome, 1)
	go func() {
		result, err := d.runTask(t.Context(), first, "claude", 0, d.logger)
		finished <- runOutcome{result, err}
	}()
	release := func() {
		t.Helper()
		if err := os.WriteFile(filepath.Join(signals, "release"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	firstFinished := false
	t.Cleanup(func() {
		release()
		if !firstFinished {
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("first fixture agent did not stop during cleanup")
			}
		}
	})
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(signals, "started"))
		return err == nil
	}, "first fixture agent started")
	second := first
	second.ID = "task-second"
	next, err := d.runTask(t.Context(), second, "claude", 1, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case done := <-finished:
		firstFinished = true
		t.Fatalf("first run ended before concurrent reuse: %+v", done)
	default:
	}
	firstCwd, err := os.ReadFile(filepath.Join(signals, "task-first.cwd"))
	if err != nil || !sameDir(t, strings.TrimSpace(string(firstCwd)), next.WorkDir) {
		t.Fatalf("concurrent runs did not share the checkout: %s %+v %v", firstCwd, next, err)
	}
	firstConfig, err := os.ReadFile(filepath.Join(signals, "task-first.config"))
	if err != nil {
		t.Fatal(err)
	}
	secondConfig, err := os.ReadFile(filepath.Join(signals, "task-second.config"))
	if err != nil || len(firstConfig) == 0 || string(firstConfig) == string(secondConfig) {
		t.Fatalf("concurrent runs shared task configuration: %s %s %v", firstConfig, secondConfig, err)
	}
	release()
	select {
	case done := <-finished:
		firstFinished = true
		if done.err != nil || done.result.BranchName == "" || done.result.WorktreeCommit == "" || next.BranchName != "" || !next.WorktreeDeliveryPending {
			t.Fatalf("concurrent branch delivery diverged: %+v %+v", done, next)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first fixture agent did not finish")
	}
	commit := worktreeTestGit(t, next.WorkDir, "rev-parse", "HEAD")
	for _, id := range []string{first.ID, second.ID} {
		if got := worktreeTestGit(t, next.WorkDir, "show", commit+":"+id+".txt"); got != id {
			t.Fatalf("shared branch lost %s's edits: %q", id, got)
		}
	}
}
