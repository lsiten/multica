package execenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestFamilyWorklinePreparationLockAcrossProcesses(t *testing.T) {
	scope := ManagedEnvProvenance{WorkspaceID: "workspace", RuntimeID: "runtime", ProjectID: "project", RepositoryScope: "repository", CodeWorkspaceIssueID: "root", IssueID: "first", AgentID: "first-agent", SquadID: "first-squad"}
	if root := os.Getenv("MULTICA_TEST_FAMILY_LOCK_ROOT"); root != "" {
		scope.IssueID, scope.AgentID, scope.SquadID = "second", "second-agent", "second-squad"
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		release, err := LockWorklinePreparation(ctx, root, scope)
		if release != nil {
			release()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("sibling process acquired family lock: %v", err)
		}
		return
	}
	root := t.TempDir()
	release, err := LockWorklinePreparation(t.Context(), root, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestFamilyWorklinePreparationLockAcrossProcesses$")
	command.Env = append(os.Environ(), "MULTICA_TEST_FAMILY_LOCK_ROOT="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cross-process family lock: %v\n%s", err, output)
	}
}
