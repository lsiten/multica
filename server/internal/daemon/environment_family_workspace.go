package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/util"
)

// The server's root candidate is the only legacy checkout eligible for adoption.
// Descendant checkouts keep their files and are never promoted into the family.
func (d *Daemon) adoptFamilyWorkspace(ctx context.Context, task Task, local *localDirectoryAssignment, heldRoot string) error {
	if task.CodeWorkspaceIssueID == "" || task.CodeWorkspaceWorkDir == "" {
		return nil
	}
	root := managedCodeRoot(d.cfg.WorkspacesRoot, task.CodeWorkspaceWorkDir)
	if root == "" {
		return nil
	}
	scope, err := execenv.ReadManagedEnvProvenance(root)
	if err != nil || scope.PrivateProviderCheckout || (scope.CodeWorkspaceIssueID != "" && scope.CodeWorkspaceIssueID != task.CodeWorkspaceIssueID) {
		return nil
	}
	fingerprint, err := repositoryScopeForTask(task)
	if err != nil {
		return err
	}
	if scope.ManagedBy != execenv.ManagedEnvProvenanceManagedBy || scope.IssueID != task.CodeWorkspaceIssueID || scope.ChatSessionID != "" || scope.WorkspaceID != task.WorkspaceID || scope.RuntimeID != task.RuntimeID || scope.ProjectID != task.ProjectID || scope.RepositoryScope != fingerprint {
		return nil
	}
	// Prove the complete legacy binding before stamping its new code owner.
	legacy := task
	legacy.CodeWorkspaceIssueID, legacy.CodeWorkspaceWorkDir = "", ""
	legacy.IssueID, legacy.AgentID, legacy.SquadID = scope.IssueID, scope.AgentID, scope.SquadID
	legacy.PriorWorkDir = task.CodeWorkspaceWorkDir
	if scope.CodeWorkspaceIssueID == "" {
		if _, ok := shouldReusePriorWorkdir(legacy, local, d.cfg.WorkspacesRoot); !ok {
			marker, err := readFamilyCodeTaskMarker(task.CodeWorkspaceWorkDir)
			if err != nil || local != nil || !marker.neutral() {
				return nil
			}
		}
	} else {
		marker, err := readFamilyCodeTaskMarker(task.CodeWorkspaceWorkDir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read retained root task context: %w", err)
		}
		if marker.neutral() {
			return nil
		}
		if marker.ManagedBy != execenv.TaskContextMarkerManagedBy || marker.AgentID != scope.AgentID || marker.IssueID != scope.IssueID {
			return errors.New("retained root task context changed")
		}
	}
	canonicalRoot, err := util.ResolveSymlinks(root)
	if err != nil {
		return err
	}
	validatedInfo, err := os.Stat(canonicalRoot)
	if err != nil {
		return err
	}
	if !sameExistingDir(canonicalRoot, heldRoot) {
		base, err := util.ResolveSymlinks(d.cfg.WorkspacesRoot)
		if err != nil {
			return err
		}
		wsRoot, err := os.OpenRoot(base)
		if err != nil {
			return err
		}
		defer wsRoot.Close()
		rel, err := filepath.Rel(base, canonicalRoot)
		if err != nil || !filepath.IsLocal(rel) {
			return errors.New("legacy root is outside managed workspaces")
		}
		claim, lockedInfo, err := d.lockEnvRootForReuseWaitingOutTheBusyWindow(ctx, wsRoot, rel, canonicalRoot, task)
		if err != nil {
			return fmt.Errorf("lock legacy family root: %w", err)
		}
		defer claim.Release()
		if claim == nil || lockedInfo == nil || !os.SameFile(validatedInfo, lockedInfo) {
			return errors.New("legacy family root changed identity before adoption")
		}
	}
	release, available, err := execenv.ReserveSharedDirectoryPaths(ctx, []string{canonicalRoot})
	if err != nil {
		return err
	}
	if !available {
		return errors.New("legacy family root still has active code consumers; retry after they finish")
	}
	defer release()
	current, err := execenv.ReadManagedEnvProvenance(canonicalRoot)
	if err != nil || *current != *scope {
		return errors.New("legacy family root ownership changed during adoption")
	}
	if info, err := os.Stat(canonicalRoot); err != nil || !os.SameFile(validatedInfo, info) {
		return errors.New("legacy family root changed identity during adoption")
	}
	if err := execenv.CleanupLegacyFamilyContext(canonicalRoot, task.CodeWorkspaceWorkDir); err != nil {
		return fmt.Errorf("remove legacy root task context: %w", err)
	}
	scope.CodeWorkspaceIssueID = task.CodeWorkspaceIssueID
	if err := execenv.WriteManagedEnvProvenance(canonicalRoot, *scope); err != nil {
		return fmt.Errorf("adopt root code workspace: %w", err)
	}
	if err := os.Remove(filepath.Join(task.CodeWorkspaceWorkDir, execenv.TaskContextMarkerRelPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type familyCodeTaskMarker struct {
	ManagedBy     string `json:"managed_by"`
	AgentID       string `json:"agent_id"`
	IssueID       string `json:"issue_id"`
	ChatSessionID string `json:"chat_session_id"`
	AutopilotID   string `json:"autopilot_id"`
}

func (m familyCodeTaskMarker) neutral() bool {
	return m.ManagedBy == execenv.TaskContextMarkerManagedBy && m.AgentID == "" && m.IssueID == "" && m.ChatSessionID == "" && m.AutopilotID == ""
}

func readFamilyCodeTaskMarker(workdir string) (familyCodeTaskMarker, error) {
	data, err := os.ReadFile(filepath.Join(workdir, execenv.TaskContextMarkerRelPath))
	var marker familyCodeTaskMarker
	if err == nil {
		err = json.Unmarshal(data, &marker)
	}
	return marker, err
}

func (d *Daemon) familyWorkspaceCandidate(ctx context.Context, task Task, local *localDirectoryAssignment, heldRoot string) (string, error) {
	if candidate := d.discoverManagedWorkdirForLocal(ctx, task, local); candidate != "" {
		task.CodeWorkspaceWorkDir = candidate
		if err := d.adoptFamilyWorkspace(ctx, task, local, heldRoot); err != nil {
			return "", err
		}
		return candidate, nil
	}
	restore := task
	restore.PriorWorkDir = task.CodeWorkspaceWorkDir
	if err := d.restoreArchivedPriorWorkdir(ctx, restore, local); err != nil {
		return "", err
	}
	if err := d.adoptFamilyWorkspace(ctx, task, local, heldRoot); err != nil {
		return "", err
	}
	if candidate := d.discoverManagedWorkdirForLocal(ctx, task, local); candidate != "" {
		return candidate, nil
	}
	return task.CodeWorkspaceWorkDir, nil
}

func (d *Daemon) retainedFamilyWorkdir(task Task, selected string) string {
	if task.CodeWorkspaceIssueID == "" || task.PriorWorkDir == "" || sameExistingDir(task.PriorWorkDir, selected) {
		return ""
	}
	root := managedCodeRoot(d.cfg.WorkspacesRoot, task.PriorWorkDir)
	scope, err := execenv.ReadManagedEnvProvenance(root)
	if err != nil || scope.WorkspaceID != task.WorkspaceID || scope.IssueID != task.IssueID || scope.AgentID != task.AgentID || scope.RuntimeID != task.RuntimeID || scope.ProjectID != task.ProjectID {
		return ""
	}
	fingerprint, err := repositoryScopeForTask(task)
	if err != nil || scope.RepositoryScope != fingerprint {
		return ""
	}
	prior, err := util.ResolveSymlinks(task.PriorWorkDir)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, prior)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return prior
}
