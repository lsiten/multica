package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/repocache"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentUncertainOperation struct {
	RequestID, Operation string
	Cause                error
	Scope                environmentMutationScope
}

func (e *environmentUncertainOperation) Error() string {
	message := fmt.Sprintf("environment %s outcome unknown (request %s): %v", e.Operation, e.RequestID, e.Cause)
	if len(e.Scope.Paths) > 0 {
		message += "; protected directory=" + e.Scope.Paths[0]
	}
	return message + "; awaiting outcome verification"
}
func (e *environmentUncertainOperation) Unwrap() error { return e.Cause }

type environmentProcessClient struct {
	daemon          *Daemon
	process         *runtimeproc.Process
	bootstrap       runtimeproc.Bootstrap
	facts           *environmentFactCallback
	gate            chan struct{}
	confirmations   chan struct{}
	mu              sync.Mutex
	reconciling     sync.Mutex
	active          int
	pending         map[string]environmentPendingOperation
	consumed        map[string]struct{}
	bindingDigest   string
	recoveryCancel  context.CancelFunc
	recoveryDone    chan struct{}
	recoveryWake    chan struct{}
	closed          bool
	acknowledgement *runtimeproc.Request
	retirementErr   error
	closeOnce       sync.Once
	closeErr        error
}

func (d *Daemon) environmentProcessMode() bool {
	return d.environmentProcessActive.Load()
}
func (d *Daemon) ensureEnvironmentProcess(ctx context.Context) (*environmentProcessClient, error) {
	d.environmentProcessMu.Lock()
	defer d.environmentProcessMu.Unlock()
	if d.environmentProcess != nil {
		return d.environmentProcess, nil
	}
	accountID, accountErr := d.verifiedAccount(ctx)
	if accountErr != nil {
		return nil, accountErr
	}
	account := struct{ ID string }{ID: accountID}
	if account.ID == "" {
		return nil, errors.New("environment owner identity missing")
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: d.cfg.ServerBaseURL, Account: account.ID, Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Service: "environment"}, d.cfg.NativeHostBuild)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(d.cfg.NativeVscreenPreferencesPath))
	if err != nil {
		return nil, err
	}
	root := filepath.Join(parent, "environment-service")
	if !filepath.IsAbs(root) {
		return nil, errors.New("environment profile root must be absolute")
	}
	if err = runtimeproc.PrepareRoot(root); err != nil {
		return nil, err
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		return nil, err
	}
	executable, err := filepath.EvalSymlinks(d.cfg.NativeHostExecutable)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(executable)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		return nil, err
	}
	environment := map[string]string{}
	for _, entry := range repocache.GitEnvironment(os.Environ()) {
		key, value, _ := strings.Cut(entry, "=")
		environment[key] = value
	}
	// Best-effort: retire a dead prior owner so a crashed service's record does
	// not permanently block this start. A live owner is never retired (the
	// owner.lock is the final gate), so this is safe even if liveness is wrong.
	if err := d.crashDetectAndReconcile(ctx, root, identity.Scope, "environment"); err != nil {
		d.logger.Warn("environment crash-detection reconcile skipped; start proceeds", "error", err)
	}
	process, err := runtimeproc.Start(context.Background(), runtimeproc.LaunchConfig{Executable: executable, SHA256: hex.EncodeToString(hash.Sum(nil)), Environment: environment, Bootstrap: bootstrap, StartupTimeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	facts, err := newEnvironmentFactCallback(d, identity.InstanceID, bootstrap.Token)
	if err != nil {
		process.Close()
		return nil, err
	}
	client := &environmentProcessClient{daemon: d, process: process, bootstrap: bootstrap, facts: facts, gate: make(chan struct{}, 1), confirmations: make(chan struct{}, 4)}
	client.startRecovery()
	if err = client.sync(ctx); err != nil {
		client.close()
		return nil, err
	}
	d.environmentProcess = client
	return client, nil
}
func (c *environmentProcessClient) sync(ctx context.Context) error {
	binding := environmentProcessBinding{WorkspacesRoot: c.daemon.cfg.WorkspacesRoot, FactsAddress: c.facts.address, Policy: environmentPolicyFromConfig(c.daemon.cfg)}
	c.daemon.mu.Lock()
	for id, workspace := range c.daemon.workspaces {
		current := environmentRuntimeBinding{WorkspaceID: id, RuntimeIDs: append([]string(nil), workspace.runtimeIDs...)}
		for repo := range workspace.allowedRepoURLs {
			current.RepoURLs = append(current.RepoURLs, repo)
		}
		for repo := range workspace.taskRepoURLs {
			if !slices.Contains(current.RepoURLs, repo) {
				current.RepoURLs = append(current.RepoURLs, repo)
			}
		}
		slices.Sort(current.RuntimeIDs)
		slices.Sort(current.RepoURLs)
		binding.Runtimes = append(binding.Runtimes, current)
	}
	c.daemon.mu.Unlock()
	slices.SortFunc(binding.Runtimes, func(left, right environmentRuntimeBinding) int {
		return strings.Compare(left.WorkspaceID, right.WorkspaceID)
	})
	encoded, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	c.mu.Lock()
	bound := c.bindingDigest == hex.EncodeToString(digest[:])
	c.mu.Unlock()
	if bound {
		return nil
	}
	return c.mutation(ctx, "environment.bind", binding, nil)
}
func (c *environmentProcessClient) close() error {
	c.closeOnce.Do(func() {
		c.stopRecovery()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c.closeErr = c.process.Stop(ctx)
		if c.closeErr != nil {
			c.closeErr = errors.Join(c.closeErr, c.process.Close())
		}
		if c.facts != nil {
			c.facts.close()
		}
	})
	return c.closeErr
}

type environmentOperationInput struct {
	WorkspaceID string                        `json:"workspace_id"`
	RuntimeID   string                        `json:"runtime_id"`
	Automatic   bool                          `json:"automatic"`
	Action      string                        `json:"action"`
	OperationID string                        `json:"operation_id"`
	Selection   protocol.EnvironmentSelection `json:"selection"`
	ArchiveID   string                        `json:"archive_id"`
}

// routeEnvironmentOperationMutation forwards a physical (Git/filesystem)
// environment mutation to the owned environment service. The child re-resolves
// the selection path and runs the same handler; its result is returned as raw
// JSON and re-encoded by the caller, preserving the exact wire shape. A
// transport failure is uncertain and is not replayed against the control parent,
// so a child that cannot complete a mutation is never silently re-run locally.
func (d *Daemon) routeEnvironmentOperationMutation(ctx context.Context, scope environmentOperationScope, request protocol.EnvironmentOperationRequest, selection protocol.EnvironmentSelection) (any, error) {
	client, err := d.ensureEnvironmentProcess(ctx)
	if err != nil {
		return nil, err
	}
	input := environmentOperationInput{WorkspaceID: scope.WorkspaceID, RuntimeID: scope.RuntimeID, Automatic: scope.Automatic, Action: request.Action, OperationID: request.ID, Selection: selection}
	var result json.RawMessage
	if err := client.mutation(ctx, "environment.operation", input, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// routeEnvironmentOperationRestore forwards a physical archive restore to the
// owned environment service. The child runs the restore and returns the typed
// result; a transport failure is uncertain and is not replayed against the
// control parent, so a restore is never silently re-run locally.
func (d *Daemon) routeEnvironmentOperationRestore(ctx context.Context, scope environmentOperationScope, archiveID string) (worktreeArchiveResult, error) {
	client, err := d.ensureEnvironmentProcess(ctx)
	if err != nil {
		return worktreeArchiveResult{}, err
	}
	input := environmentOperationInput{WorkspaceID: scope.WorkspaceID, RuntimeID: scope.RuntimeID, Automatic: scope.Automatic, Action: "restore", ArchiveID: archiveID}
	var result worktreeArchiveResult
	if err := client.mutation(ctx, "environment.operation", input, &result); err != nil {
		return worktreeArchiveResult{}, err
	}
	return result, nil
}
