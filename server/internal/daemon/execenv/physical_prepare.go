package execenv

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// PhysicalPrepareParams contains only checkout identity and physical policy.
// Provider configuration, task text, MCP credentials and environment values do not belong here.
type PhysicalPrepareParams struct {
	WorkspacesRoot          string               `json:"-"`
	WorkspaceID             string               `json:"workspace_id"`
	WorkspaceSlug           string               `json:"workspace_slug"`
	TaskID                  string               `json:"task_id"`
	RuntimeID               string               `json:"runtime_id"`
	IssueIdentifier         string               `json:"issue_identifier"`
	AgentID                 string               `json:"agent_id"`
	AgentName               string               `json:"agent_name"`
	ProjectID               string               `json:"project_id"`
	SquadID                 string               `json:"squad_id"`
	IssueID                 string               `json:"issue_id"`
	ChatSessionID           string               `json:"chat_session_id"`
	AutopilotID             string               `json:"autopilot_id"`
	RepositoryScope         string               `json:"repository_scope"`
	ConversationKey         string               `json:"conversation_key"`
	ConversationID          string               `json:"conversation_id"`
	LocalWorkDir            string               `json:"local_work_dir"`
	LocalWorktree           *LocalWorktreeParams `json:"local_worktree,omitempty"`
	PrivateCheckoutRequired bool                 `json:"private_checkout_required"`
}

// PhysicalPreparationInput derives public physical facts inside the trusted task owner.
func PhysicalPreparationInput(params PrepareParams) (PhysicalPrepareParams, error) {
	fingerprint, err := RepositoryScopeFingerprint(params.Task.Repos, params.Task.ProjectResources)
	if err != nil {
		return PhysicalPrepareParams{}, err
	}
	key, id := localWorktreeConversation(params)
	return PhysicalPrepareParams{WorkspacesRoot: params.WorkspacesRoot, WorkspaceID: params.WorkspaceID, WorkspaceSlug: params.WorkspaceSlug, TaskID: params.TaskID, RuntimeID: params.RuntimeID, IssueIdentifier: params.IssueIdentifier, AgentID: params.Task.AgentID, AgentName: params.AgentName, ProjectID: params.Task.ProjectID, SquadID: params.Task.SquadID, IssueID: params.Task.IssueID, ChatSessionID: params.Task.ChatSessionID, AutopilotID: params.Task.AutopilotID, RepositoryScope: fingerprint, ConversationKey: key, ConversationID: id, LocalWorkDir: params.LocalWorkDir, LocalWorktree: params.LocalWorktree, PrivateCheckoutRequired: params.IsolateLocalContext && NeedsPrivateProviderCheckout(params.Provider, params.McpConfig) && (params.LocalWorkDir != "" || params.LocalWorktree != nil)}, nil
}

// PreparePhysical acquires the root and prepares only physical checkout state.
// The returned claim must remain held until a durable worker handoff is published.
func PreparePhysical(params PhysicalPrepareParams, logger *slog.Logger) (*Environment, error) {
	return preparePhysical(params, false, logger)
}

func preparePhysical(params PhysicalPrepareParams, preclaimed bool, logger *slog.Logger) (*Environment, error) {
	if params.WorkspacesRoot == "" {
		return nil, fmt.Errorf("execenv: workspaces root is required")
	}
	if params.WorkspaceID == "" {
		return nil, fmt.Errorf("execenv: workspace ID is required")
	}
	if params.TaskID == "" {
		return nil, fmt.Errorf("execenv: task ID is required")
	}
	privateProvider := params.PrivateCheckoutRequired
	privateCopySource := ""
	if privateProvider {
		if params.LocalWorktree != nil {
			copy := *params.LocalWorktree
			copy.PrivateCheckout = true
			params.LocalWorktree = &copy
		} else if _, git := detectGitRepo(params.LocalWorkDir); git {
			params.LocalWorktree = &LocalWorktreeParams{LocalPath: params.LocalWorkDir, PrivateCheckout: true, RetainCheckout: true}
			params.LocalWorkDir = ""
		} else {
			privateCopySource, params.LocalWorkDir = params.LocalWorkDir, ""
		}
	}

	envRoot, err := ResolveRootDir(RootDirParams{
		WorkspacesRoot:  params.WorkspacesRoot,
		WorkspaceID:     params.WorkspaceID,
		WorkspaceSlug:   params.WorkspaceSlug,
		TaskID:          params.TaskID,
		IssueIdentifier: params.IssueIdentifier,
	})
	if err != nil {
		return nil, err
	}

	// Self-heal the root-level daemon marker on every task start so a marker
	// removed while the daemon runs is restored before the agent spawns. The
	// per-workdir marker written below only covers cwds inside the workdir;
	// the root marker keeps the CLI fail-closed guard active for subprocesses
	// that lose all MULTICA_* env vars AND escape above the workdir. Non-fatal:
	// without it the workdir marker still protects the common case.
	if err := EnsureWorkspacesRootMarker(params.WorkspacesRoot); err != nil && logger != nil {
		logger.Warn("execenv: workspaces root marker not written; fail-closed guard limited to the task workdir", "error", err)
	}

	// Take exclusive ownership of the env root before touching anything in it.
	// What follows wipes the directory, and while the segment was a UUIDv7
	// prefix that routinely wiped a live sibling task's workdir, worktree and
	// task-scoped config (#7326). taskKey now reads the id's random tail, which
	// makes a shared path improbable rather than impossible — so prove
	// ownership instead of assuming it. A task that refuses to start is
	// recoverable; one that deletes a running sibling's uncommitted work is not.
	//
	// claimEnvRoot is the only thing standing between two same-key tasks, so it
	// has to be atomic end to end: a read-then-delete would let both pass the
	// check and one still delete the other. Once claimed, the claim is held for
	// the rest of Prepare — the reset below clears the directory's CONTENTS and
	// leaves the marker in place, so there is never a moment where the env root
	// looks unowned to a racing task.
	var lockFile *os.File
	lockClaimed := false
	if preclaimed {
		// The caller holds the claim and already reset the root; just make sure
		// the directory is there before populating it.
		if err := os.MkdirAll(envRoot, 0o755); err != nil {
			return nil, fmt.Errorf("execenv: create env root %s: %w", envRoot, err)
		}
	} else {
		lock, reset, err := claimEnvRoot(envRoot, params.WorkspaceID, params.TaskID)
		if err != nil {
			return nil, fmt.Errorf("execenv: %w", err)
		}
		lockFile = lock
		// Release the lock on every failure path below. The successful path
		// hands it to the Environment.
		lockClaimed = true
		defer func() {
			if lockClaimed {
				releaseLockFile(lockFile)
			}
		}()
		// reset means this task already owned the directory and the execution
		// that left it there is gone — a rerun, which is meant to start from a
		// clean tree. Reuse of a PRIOR task's directory never reaches here;
		// that is Reuse, which takes an explicit WorkDir and deletes nothing.
		if reset {
			if err := resetEnvRootContents(envRoot); err != nil {
				return nil, fmt.Errorf("execenv: reset existing env: %w", err)
			}
		}
	}

	// Create directory tree. For the standard flow the agent's workdir is
	// envRoot/workdir; for local_directory tasks the user's path takes its
	// place and we only need to create the scratch directories under
	// envRoot.
	workDir := filepath.Join(envRoot, "workdir")
	scratchDirs := []string{filepath.Join(envRoot, "output"), filepath.Join(envRoot, "logs")}
	if params.LocalWorkDir == "" && params.LocalWorktree == nil {
		scratchDirs = append(scratchDirs, workDir)
	} else if params.LocalWorkDir != "" {
		workDir = params.LocalWorkDir
	}
	for _, dir := range scratchDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("execenv: create directory %s: %w", dir, err)
		}
	}
	multicaConfigRoot := filepath.Join(envRoot, "multica-config")
	if err := WriteReviewRuntime(envRoot, ReviewRuntime{WorkspaceID: params.WorkspaceID, TaskID: params.TaskID, RuntimeID: params.RuntimeID, AgentID: params.AgentID, AgentName: params.AgentName}); err != nil {
		return nil, fmt.Errorf("execenv: record task runtime: %w", err)
	}

	// Worktree mode: build the task's own checkout of the user's repo inside
	// envRoot and use it as the workdir. Done before any context file is
	// written so the sidecars land inside the disposable worktree instead of
	// the user's directory.
	var localWorktree *LocalWorktree
	// Tracks whether Prepare reached its successful return. Everything after
	// worktree creation can still fail — context files, provider homes, MCP
	// config — and on those paths the caller never receives an Environment, so
	// nothing downstream knows a worktree exists to clean up. Without the
	// rollback below, each such failure would leave a registration in the
	// user's repo and a branch that no task ever ran in.
	prepareSucceeded := false
	if params.LocalWorktree != nil {
		wtParams := *params.LocalWorktree
		wtParams.EnvRoot = envRoot
		wtParams.AgentName = params.AgentName
		wtParams.TaskID = params.TaskID
		wtParams.ConversationKey, wtParams.ConversationID = params.ConversationKey, params.ConversationID
		wtParams.WorkspaceID = params.WorkspaceID
		wtParams.AgentID = params.AgentID
		wtParams.ProjectID, wtParams.SquadID, wtParams.RuntimeID = params.ProjectID, params.SquadID, params.RuntimeID
		wtParams.RepositoryScope = params.RepositoryScope
		var err error
		localWorktree, err = PrepareLocalWorktree(wtParams, logger)
		if err != nil {
			return nil, err
		}
		defer func() {
			if prepareSucceeded {
				return
			}
			// Safe to discard unconditionally: no agent has run yet, so the
			// worktree holds only what Prepare itself put there.
			localWorktree.Discard(logger)
		}()
		workDir = localWorktree.WorkDir
		// The resource may point at a subdirectory that holds only ignored
		// files, in which case git doesn't materialise it in the worktree.
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, fmt.Errorf("execenv: create worktree workdir %s: %w", workDir, err)
		}
	}

	env := &Environment{
		RootDir:                 envRoot,
		PrivateProviderCheckout: privateProvider,
		WorkDir:                 workDir,
		LocalDirectory:          params.LocalWorkDir != "",
		LocalWorktree:           localWorktree,
		MulticaConfigRoot:       multicaConfigRoot,
		logger:                  logger,
		lockFile:                lockFile,
	}
	if privateCopySource != "" {
		if err := os.CopyFS(workDir, os.DirFS(privateCopySource)); err != nil {
			return nil, fmt.Errorf("copy private provider workspace: %w", err)
		}
	}

	// Persist managed-env provenance for non-local resumable envs at Prepare time
	// (not on completion, where .gc_meta.json is written). A same-issue
	// follow-up can be claimed the instant the prior task completes — before
	// the prior handler writes .gc_meta.json — so reuse eligibility must be
	// provable from an artifact that exists the moment the env is created. Only
	// managed (non-local_directory) issue, chat and automation envs get this marker;
	// each has a durable workline identity. Non-fatal: a write failure
	// only costs the next follow-up its session reuse (it falls back to a fresh
	// session), which must never block dispatching this task.
	if params.LocalWorkDir == "" && (params.IssueID != "" || params.ChatSessionID != "" || params.AutopilotID != "") {
		repositoryScope := params.RepositoryScope
		if err := WriteManagedEnvProvenance(envRoot, ManagedEnvProvenance{
			WorkspaceID:     params.WorkspaceID,
			RuntimeID:       params.RuntimeID,
			ProjectID:       params.ProjectID,
			SquadID:         params.SquadID,
			RepositoryScope: repositoryScope,
			IssueID:         params.IssueID,
			ChatSessionID:   params.ChatSessionID,
			AutopilotID:     params.AutopilotID,
			AgentID:         params.AgentID,
			AgentName:       params.AgentName,
		}); err != nil && logger != nil {
			logger.Warn("execenv: write managed env provenance failed (non-fatal); a follow-up may start a fresh session", "error", err)
		}
	}

	if env.LocalDirectory {
		if err := WriteReviewDirectory(envRoot, ReviewDirectory{WorkspaceID: params.WorkspaceID, TaskID: params.TaskID, Path: env.WorkDir}); err != nil && logger != nil {
			logger.Warn("execenv: local review directory binding unavailable", "error", err)
		}
	}

	prepareSucceeded = true
	lockClaimed = false
	return env, nil
}

// PhysicalReuseParams contains only the verified source and service-owned worktree.
// ReusedLocalWorktree is internal physical settlement state, never a wire input.
type PhysicalReuseParams struct {
	WorkspacesRoot      string
	WorkDir             string
	RunRoot             string
	LocalDirectory      bool
	ReusedLocalWorktree *LocalWorktree
}

func reusePhysical(params PhysicalReuseParams, logger *slog.Logger) *Environment {
	if _, err := os.Stat(params.WorkDir); err != nil {
		return nil
	}

	// Self-heal the root-level daemon marker on the reuse path too, so a marker
	// removed while the daemon runs is restored before a reused task spawns —
	// otherwise reuse could run without the fail-closed guard until the next
	// fresh Prepare. Non-fatal: the per-workdir marker still protects the common
	// case, and an empty WorkspacesRoot (legacy callers) simply skips this.
	if params.WorkspacesRoot != "" {
		if err := EnsureWorkspacesRootMarker(params.WorkspacesRoot); err != nil && logger != nil {
			logger.Warn("execenv: workspaces root marker not written on reuse; fail-closed guard limited to the task workdir", "error", err)
		}
	}

	rootDir := filepath.Dir(params.WorkDir)
	codeRoot := rootDir
	var reusedWorktree *LocalWorktree
	if params.ReusedLocalWorktree != nil {
		reusedWorktree = params.ReusedLocalWorktree
		codeRoot = filepath.Dir(reusedWorktree.Path)
		params.WorkDir = reusedWorktree.WorkDir
	}
	if params.RunRoot != "" {
		owner, err := ReadEnvRootOwner(params.RunRoot)
		if err != nil || owner == nil || owner.TaskID == "" || owner.WorkspaceID == "" || ValidateEnvRootOwnerPath(params.WorkspacesRoot, params.RunRoot, *owner) != nil {
			logger.Warn("execenv: per-run configuration root has no valid owner")
			return nil
		}
		rootDir = params.RunRoot
	}
	if params.LocalDirectory {
		// For local_directory tasks the user's WorkDir is unrelated to
		// envRoot (envRoot still lives under workspacesRoot/{wsID}/...),
		// so reading it from filepath.Dir(WorkDir) would point at the
		// parent of the user's directory. Callers that need a real
		// RootDir on the reuse path should arrange to pass it in
		// explicitly; for v1 the daemon only ever reuses local_directory
		// workdirs after a fresh Prepare in the same task lifetime, so
		// the empty RootDir on reuse is fine for the current callers
		// (GC writes meta from Prepare's result, not Reuse's).
		rootDir = ""
	}
	env := &Environment{
		RootDir:        rootDir,
		WorkDir:        params.WorkDir,
		LocalDirectory: params.LocalDirectory,
		logger:         logger,
		LocalWorktree:  reusedWorktree,
	}
	if codeRoot != rootDir && !params.LocalDirectory {
		env.CodeRootDir = codeRoot
	}
	return env
}

// PreparePhysicalClaimed continues a service-reserved root already held by its
// task owner. It verifies the exact reservation rather than trusting a wire flag.
func PreparePhysicalClaimed(params PhysicalPrepareParams, reservation PhysicalRootReservation, identity os.FileInfo, logger *slog.Logger) (*Environment, error) {
	if params.TaskID != reservation.TaskID || params.WorkspaceID != reservation.WorkspaceID {
		return nil, fmt.Errorf("physical root reservation scope mismatch")
	}
	if err := VerifyPhysicalRootClaim(reservation, identity); err != nil {
		return nil, err
	}
	return preparePhysical(params, true, logger)
}

// ReusePhysical exposes physical validation only to the environment owner.
func ReusePhysical(params PhysicalReuseParams, logger *slog.Logger) *Environment {
	return reusePhysical(params, logger)
}
