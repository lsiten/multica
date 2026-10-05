package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/util"
)

func (d *Daemon) acquireReusedWorktreeSnapshot(ctx context.Context, task Task, local *localDirectoryAssignment, logger *slog.Logger) (func(), error) {
	return d.localPathLocks.TrackShared(ctx, local.RealPath, task.ID)
}
func managedCodeRoot(workspacesRoot, workdir string) string {
	root, err := util.ResolveSymlinks(workspacesRoot)
	if err != nil {
		return ""
	}
	path, err := util.ResolveSymlinks(workdir)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 3 {
		return ""
	}
	return filepath.Join(root, parts[0], parts[1])
}

func localWorktreeParamsForTask(task Task, local *localDirectoryAssignment, root string) execenv.LocalWorktreeParams {
	key, id := execenv.LocalWorktreeConversation(execenv.PrepareParams{IssueIdentifier: task.IssueIdentifier, Task: execenv.TaskContextForEnv{IssueID: task.IssueID, ChatSessionID: task.ChatSessionID, AutopilotID: task.AutopilotID}})
	name := "agent"
	if task.Agent != nil {
		name = task.Agent.Name
	}
	repositoryScope, _ := execenv.RepositoryScopeFingerprint(convertReposForEnv(task.Repos), convertProjectResourcesForEnv(task.ProjectResources))
	return execenv.LocalWorktreeParams{LocalPath: local.AbsPath, EnvRoot: root, AgentName: name, TaskID: task.ID, ConversationKey: key, ConversationID: id, WorkspaceID: task.WorkspaceID, AgentID: task.AgentID, RetainCheckout: true, ProjectID: task.ProjectID, SquadID: task.SquadID, RuntimeID: task.RuntimeID, RepositoryScope: repositoryScope}
}
