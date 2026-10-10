package daemon

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func repositoryScopeForTask(task Task) (string, error) {
	if task.physicalRepositoryScope != "" {
		decoded, err := hex.DecodeString(task.physicalRepositoryScope)
		if err != nil || len(decoded) != 32 {
			return "", errors.New("invalid physical repository fingerprint")
		}
		return task.physicalRepositoryScope, nil
	}
	return execenv.RepositoryScopeFingerprint(convertReposForEnv(task.Repos), convertProjectResourcesForEnv(task.ProjectResources))
}

func managedScopeForTask(task Task) (execenv.ManagedEnvProvenance, error) {
	fingerprint, err := repositoryScopeForTask(task)
	return execenv.ManagedEnvProvenance{WorkspaceID: task.WorkspaceID, RuntimeID: task.RuntimeID, AgentID: task.AgentID, IssueID: task.IssueID, ChatSessionID: task.ChatSessionID, AutopilotID: task.AutopilotID, ProjectID: task.ProjectID, SquadID: task.SquadID, RepositoryScope: fingerprint}, err
}

// Directory discovery is a recovery path for absent/stale PriorWorkDir, not an
// invitation to reuse the newest directory belonging to a different task scope.
func (d *Daemon) discoverManagedWorkdir(ctx context.Context, task Task) string {
	paths, err := d.environmentRootPaths(ctx)
	if err != nil {
		return ""
	}
	type candidate struct {
		path     string
		modified int64
	}
	candidates := []candidate{}
	for _, path := range paths {
		if ctx.Err() != nil {
			return ""
		}
		scope, err := execenv.ReadManagedEnvProvenance(path)
		if err != nil || !managedReuseScopeMatches(task, scope) {
			continue
		}
		workdir := filepath.Join(path, "workdir")
		info, err := os.Stat(workdir)
		if err != nil || !info.IsDir() {
			continue
		}
		copy := task
		copy.PriorWorkDir = workdir
		if verified, ok := shouldReusePriorWorkdir(copy, nil, d.cfg.WorkspacesRoot); ok {
			candidates = append(candidates, candidate{verified, info.ModTime().UnixNano()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modified > candidates[j].modified })
	if len(candidates) > 0 {
		return candidates[0].path
	}
	return ""
}
