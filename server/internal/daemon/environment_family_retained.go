package daemon

import (
	"errors"
	"fmt"
	"os"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func (d *Daemon) preserveReplacedFamilyWorkspace(task Task, selected string) (string, error) {
	prior := d.retainedFamilyWorkdir(task, selected)
	if prior == "" {
		return "", nil
	}
	selectedRoot := managedCodeRoot(d.cfg.WorkspacesRoot, selected)
	scope, err := execenv.ReadManagedEnvProvenance(selectedRoot)
	if err != nil || !managedReuseScopeMatches(task, scope) {
		return "", nil
	}
	marker := execenv.RetainedFamilyWorkspace{WorkspaceID: task.WorkspaceID, IssueID: task.IssueID, CodeWorkspaceIssueID: task.CodeWorkspaceIssueID, SelectedWorkDir: selected}
	root := managedCodeRoot(d.cfg.WorkspacesRoot, prior)
	if err := execenv.WriteRetainedFamilyWorkspace(root, marker); err != nil {
		return "", fmt.Errorf("preserve previous issue workspace: %w", err)
	}
	return prior, nil
}

func retainedFamilyWorkspaceReason(path string) string {
	marker, err := execenv.ReadRetainedFamilyWorkspace(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		return "unavailable"
	}
	scope, err := execenv.ReadManagedEnvProvenance(path)
	if err != nil || marker.WorkspaceID != scope.WorkspaceID || marker.IssueID != scope.IssueID || marker.CodeWorkspaceIssueID == "" {
		return "unavailable"
	}
	// The replacement may itself have been archived. Retain the original until
	// a member explicitly reviews or discards it, regardless of replacement life.
	return "retained"
}
