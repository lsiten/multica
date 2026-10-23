package daemon

import (
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Report only the checkout actually selected, after verifying its managed owner.
func (d *Daemon) selectedCodeWorkspace(task Task, env *execenv.Environment) *protocol.CodeWorkspaceSelection {
	if task.CodeWorkspaceIssueID == "" || task.IssueID == "" || task.ChatSessionID != "" || task.AutopilotID != "" || env == nil || env.PrivateProviderCheckout {
		return nil
	}
	root := managedCodeRoot(d.cfg.WorkspacesRoot, env.WorkDir)
	if root == "" {
		return nil
	}
	scope, err := execenv.ReadManagedEnvProvenance(root)
	if err != nil || !managedReuseScopeMatches(task, scope) {
		return nil
	}
	return &protocol.CodeWorkspaceSelection{IssueID: task.CodeWorkspaceIssueID, RuntimeID: task.RuntimeID, WorkDir: env.WorkDir}
}
