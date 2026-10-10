package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/repocache"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentRuntimeBinding struct {
	WorkspaceID string   `json:"workspace_id"`
	RuntimeIDs  []string `json:"runtime_ids"`
	RepoURLs    []string `json:"repo_urls"`
}
type environmentProcessPolicy struct {
	GCEnabled                  bool          `json:"gc_enabled"`
	GCInterval                 time.Duration `json:"gc_interval"`
	GCTTL                      time.Duration `json:"gc_ttl"`
	GCCompletedTaskTTL         time.Duration `json:"gc_completed_task_ttl"`
	GCOrphanTTL                time.Duration `json:"gc_orphan_ttl"`
	GCArtifactTTL              time.Duration `json:"gc_artifact_ttl"`
	GCArtifactPatterns         []string      `json:"gc_artifact_patterns"`
	GCRepoTTL                  time.Duration `json:"gc_repo_ttl"`
	GCRepoMaintenanceEnabled   bool          `json:"gc_repo_maintenance_enabled"`
	GCCodexSessionTTL          time.Duration `json:"gc_codex_session_ttl"`
	GCHermesMemoryTTL          time.Duration `json:"gc_hermes_memory_ttl"`
	GCHermesSessionTTL         time.Duration `json:"gc_hermes_session_ttl"`
	GCTaskTempLegacyTTL        time.Duration `json:"gc_task_temp_legacy_ttl"`
	EnvironmentArchiveTTL      time.Duration `json:"environment_archive_ttl"`
	EnvironmentRecycleEnabled  bool          `json:"environment_recycle_enabled"`
	EnvironmentRecycleInterval time.Duration `json:"environment_recycle_interval"`
	KeepEnvAfterTask           bool          `json:"keep_env_after_task"`
	WorktreeStaleTTL           time.Duration `json:"worktree_stale_ttl"`
}

func environmentPolicyFromConfig(c Config) environmentProcessPolicy {
	return environmentProcessPolicy{GCEnabled: c.GCEnabled, GCInterval: c.GCInterval, GCTTL: c.GCTTL, GCCompletedTaskTTL: c.GCCompletedTaskTTL, GCOrphanTTL: c.GCOrphanTTL, GCArtifactTTL: c.GCArtifactTTL, GCArtifactPatterns: append([]string(nil), c.GCArtifactPatterns...), GCRepoTTL: c.GCRepoTTL, GCRepoMaintenanceEnabled: c.GCRepoMaintenanceEnabled, GCCodexSessionTTL: c.GCCodexSessionTTL, GCHermesMemoryTTL: c.GCHermesMemoryTTL, GCHermesSessionTTL: c.GCHermesSessionTTL, GCTaskTempLegacyTTL: c.GCTaskTempLegacyTTL, EnvironmentArchiveTTL: c.EnvironmentArchiveTTL, EnvironmentRecycleEnabled: c.EnvironmentRecycleEnabled, EnvironmentRecycleInterval: c.EnvironmentRecycleInterval, KeepEnvAfterTask: c.KeepEnvAfterTask, WorktreeStaleTTL: c.WorktreeStaleTTL}
}
func (p environmentProcessPolicy) apply(c *Config) error {
	if p.GCEnabled && p.GCInterval <= 0 || p.EnvironmentRecycleEnabled && p.EnvironmentRecycleInterval <= 0 {
		return errors.New("environment maintenance interval must be positive")
	}
	c.GCEnabled = p.GCEnabled
	c.GCInterval = p.GCInterval
	c.GCTTL = p.GCTTL
	c.GCCompletedTaskTTL = p.GCCompletedTaskTTL
	c.GCOrphanTTL = p.GCOrphanTTL
	c.GCArtifactTTL = p.GCArtifactTTL
	c.GCArtifactPatterns = append([]string(nil), p.GCArtifactPatterns...)
	c.GCRepoTTL = p.GCRepoTTL
	c.GCRepoMaintenanceEnabled = p.GCRepoMaintenanceEnabled
	c.GCCodexSessionTTL = p.GCCodexSessionTTL
	c.GCHermesMemoryTTL = p.GCHermesMemoryTTL
	c.GCHermesSessionTTL = p.GCHermesSessionTTL
	c.GCTaskTempLegacyTTL = p.GCTaskTempLegacyTTL
	c.EnvironmentArchiveTTL = p.EnvironmentArchiveTTL
	c.EnvironmentRecycleEnabled = p.EnvironmentRecycleEnabled
	c.EnvironmentRecycleInterval = p.EnvironmentRecycleInterval
	c.KeepEnvAfterTask = p.KeepEnvAfterTask
	c.WorktreeStaleTTL = p.WorktreeStaleTTL
	return nil
}

type environmentProcessBinding struct {
	Policy         environmentProcessPolicy    `json:"policy"`
	WorkspacesRoot string                      `json:"workspaces_root"`
	FactsAddress   string                      `json:"facts_address"`
	Runtimes       []environmentRuntimeBinding `json:"runtimes"`
}
type environmentApplicationSource struct {
	ResourceID    string          `json:"resource_id"`
	WorkspaceID   string          `json:"workspace_id"`
	RuntimeID     string          `json:"runtime_id"`
	ApplicationID string          `json:"application_id"`
	InstanceID    string          `json:"instance_id"`
	Revision      int64           `json:"revision"`
	ResourceType  string          `json:"resource_type"`
	ResourceRef   json.RawMessage `json:"resource_ref"`
	Ref           string          `json:"ref"`
	WorkDir       string          `json:"work_dir"`
}
type environmentProcessService struct {
	life           context.Context
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	policyJSON     []byte
	mu             sync.RWMutex
	bootstrap      runtimeproc.Bootstrap
	daemon         *Daemon
	physical       *environmentPhysicalOwner
	factsTransport *http.Transport
	factsAddress   string
}

// RunEnvironmentService owns physical workspace operations. Private task inputs
// are rejected by its typed operations and never passed to execenv preparation.
func RunEnvironmentService(ctx context.Context, b runtimeproc.Bootstrap) error {
	if b.Identity.Scope.Service != "environment" {
		return errors.New("unsupported environment role")
	}
	if err := runtimeproc.PrepareRoot(b.Root); err != nil {
		return err
	}
	lock, err := lockMirrorProcessDomain(filepath.Join(b.Root, "environment-owner.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &Daemon{environmentServiceOwner: true, envRootBusyWait: 15 * time.Second, cfg: Config{ServerBaseURL: b.Identity.Scope.Backend, DaemonID: b.Identity.Scope.DaemonID, Profile: b.Identity.Scope.Profile}, logger: logger, workspaces: map[string]*workspaceState{}, runtimeIndex: map[string]Runtime{}, localPathLocks: NewLocalPathLocker(), activeStores: map[string]int{}, deletingStores: map[string]bool{}, activeEnvRoots: map[string]int{}, deletingEnvRoots: map[string]bool{}}
	d.activeStoresCond = sync.NewCond(&d.activeStoresMu)
	d.activeEnvRootsCond = sync.NewCond(&d.activeEnvRootsMu)
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	owner := &environmentProcessService{bootstrap: b, daemon: d, life: life, cancel: cancel}
	defer owner.shutdown(context.Background())
	service, err := runtimeproc.NewService(runtimeproc.Config{Bootstrap: b, Capabilities: []string{"environment.bind", "cache.sync", "cache.coauthor", "cache.checkout", "physical.prepare", "physical.begin_task", "physical.root_confirm", "physical.root_claim", "physical.select", "physical.attach", "physical.abort", "physical.finish_begin", "physical_finish_confirm", "physical.verify_completion", "application.source", "environment.command", "review.run", "environment.gc", "worktree-delivery.ack"}, ConcurrentCapabilities: []string{"physical_finish_confirm"}, ReadCapabilities: []string{"cache.lookup", "environment.inventory", "worktree-delivery.receipts"}, Handler: owner.mutate, ReadHandler: owner.read, Shutdown: owner.shutdown})
	if err != nil {
		return err
	}
	return service.Serve(ctx)
}

func decodeEnvironmentPayload(raw json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("environment operation has trailing content")
	}
	return nil
}
func environmentOperationResult(value any, err error) (json.RawMessage, *runtimeproc.Error) {
	encoded, encodeErr := json.Marshal(value)
	if encodeErr != nil {
		return nil, &runtimeproc.Error{Code: "encoding", Message: "environment result could not be encoded"}
	}
	if err != nil {
		return encoded, &runtimeproc.Error{Code: "environment_failed", Message: err.Error()}
	}
	return encoded, nil
}

func (s *environmentProcessService) bind(binding environmentProcessBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !filepath.IsAbs(binding.WorkspacesRoot) || validateApplicationCallbackAddress(binding.FactsAddress) != nil {
		return errors.New("invalid environment service binding")
	}
	policyJSON, err := json.Marshal(binding.Policy)
	if err != nil {
		return err
	}
	if s.physical != nil && !bytes.Equal(policyJSON, s.policyJSON) {
		return errors.New("environment policy requires a drained service restart")
	}
	if err := os.MkdirAll(binding.WorkspacesRoot, 0700); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(binding.WorkspacesRoot)
	if err != nil {
		return err
	}
	if s.physical != nil && (s.daemon.cfg.WorkspacesRoot != canonical || s.factsAddress != binding.FactsAddress) {
		return errors.New("environment service root cannot change")
	}
	firstBind := s.physical == nil
	if s.physical == nil {
		physical, err := newEnvironmentPhysicalOwner(canonical, filepath.Join(s.bootstrap.Root, "physical-preparations"), s.daemon.logger)
		if err != nil {
			return err
		}
		transport := &http.Transport{Proxy: nil}
		client := NewClient("http://physical-facts.invalid")
		client.token = s.bootstrap.Token
		facts := &environmentFactTransport{address: binding.FactsAddress, instanceID: s.bootstrap.Identity.InstanceID, token: s.bootstrap.Token, http: &http.Client{Transport: transport}}
		client.client = &http.Client{Transport: facts}
		s.factsTransport = transport
		s.factsAddress = binding.FactsAddress
		s.daemon.client = client
		s.daemon.cfg.WorkspacesRoot = canonical
		if err := execenv.EnsureWorkspacesRootMarker(canonical); err != nil {
			return err
		}
		s.daemon.repoCache = repocache.NewWithRepositoryAuth(filepath.Join(canonical, ".repos"), s.daemon.logger, environmentRepositoryAuth{facts: facts})
		if err := binding.Policy.apply(&s.daemon.cfg); err != nil {
			transport.CloseIdleConnections()
			return err
		}
		physical.releaseConsumer = s.releasePhysicalConsumer
		// Release the maintenance barrier held while the worker's Git user was
		// active, exactly once, when the finish outcome is durably recorded.
		physical.onSettle = func() { s.daemon.activeTasks.Add(-1) }
		s.physical = physical
		s.policyJSON = policyJSON
	}
	workspaces := map[string]*workspaceState{}
	runtimes := map[string]Runtime{}
	for _, binding := range binding.Runtimes {
		if binding.WorkspaceID == "" {
			return errors.New("environment workspace identity missing")
		}
		workspace := &workspaceState{workspaceID: binding.WorkspaceID, runtimeIDs: append([]string(nil), binding.RuntimeIDs...), allowedRepoURLs: map[string]struct{}{}}
		for _, repo := range binding.RepoURLs {
			workspace.allowedRepoURLs[repo] = struct{}{}
		}
		workspaces[binding.WorkspaceID] = workspace
		for _, runtimeID := range binding.RuntimeIDs {
			if runtimeID == "" {
				return errors.New("environment runtime identity missing")
			}
			runtimes[runtimeID] = Runtime{ID: runtimeID}
		}
	}
	s.daemon.mu.Lock()
	s.daemon.workspaces = workspaces
	s.daemon.runtimeIndex = runtimes
	s.daemon.mu.Unlock()
	// Start maintenance loops only after the runtime/workspace maps are fully
	// populated, so the first recycle scan cannot observe a partially bound
	// service. The GC loop's startup delay and the recycle loop's immediate scan
	// both read the maps, so ordering them after the map assignment closes the
	// partial-initialization window for both.
	if firstBind && s.life != nil {
		s.workers.Add(2)
		go func() { defer s.workers.Done(); s.daemon.gcLoop(s.life) }()
		go func() { defer s.workers.Done(); s.daemon.environmentRecycleLoop(s.life) }()
	}
	return nil
}
func (s *environmentProcessService) allowsRepo(workspaceID, repo string) bool {
	s.daemon.mu.Lock()
	defer s.daemon.mu.Unlock()
	workspace := s.daemon.workspaces[workspaceID]
	if workspace == nil {
		return false
	}
	_, allowed := workspace.allowedRepoURLs[repo]
	return allowed
}

func (s *environmentProcessService) mutate(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	if request.Operation == "environment.bind" {
		var binding environmentProcessBinding
		if err := decodeEnvironmentPayload(request.Payload, &binding); err != nil {
			return environmentOperationResult(nil, err)
		}
		return environmentOperationResult(struct{}{}, s.bind(binding))
	}
	s.mu.RLock()
	bound := s.physical != nil
	s.mu.RUnlock()
	if !bound {
		return environmentOperationResult(nil, errors.New("environment service is not bound"))
	}
	switch request.Operation {
	case "physical.begin_task":
		var input struct {
			Physical execenv.PhysicalPrepareParams `json:"physical"`
			Metadata *execenv.GCMeta               `json:"metadata,omitempty"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		if !s.daemon.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: input.Physical.WorkspaceID, RuntimeID: input.Physical.RuntimeID}) {
			return environmentOperationResult(nil, errors.New("physical runtime is not owned"))
		}
		result, err := s.physical.prepare(ctx, input.Physical, input.Metadata)
		return environmentOperationResult(result, err)
	case "physical.prepare":
		var input execenv.PhysicalPrepareParams
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		if !s.daemon.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID}) {
			return environmentOperationResult(nil, errors.New("physical runtime is not owned"))
		}
		result, err := s.physical.prepare(ctx, input)
		return environmentOperationResult(result, err)
	case "physical.root_claim":
		var reservation execenv.PhysicalRootReservation
		if err := decodeEnvironmentPayload(request.Payload, &reservation); err != nil {
			return environmentOperationResult(nil, err)
		}
		return environmentOperationResult(struct{}{}, s.physical.claimRoot(reservation))
	case "physical.select":
		var input environmentPhysicalSelection
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		result, err := s.selectPhysical(ctx, input)
		return environmentOperationResult(result, err)
	case "physical.attach":
		var input struct {
			PreparationID string                      `json:"preparation_id"`
			Participant   execenv.PhysicalParticipant `json:"participant"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		return environmentOperationResult(struct{}{}, s.attachPhysical(ctx, input.PreparationID, input.Participant))
	case "physical.abort":
		var input struct {
			PreparationID string `json:"preparation_id"`
			PrivateClean  bool   `json:"private_clean"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		return environmentOperationResult(struct{}{}, s.abortPhysical(ctx, input.PreparationID, input.PrivateClean))
	case "physical.root_confirm":
		var reservation execenv.PhysicalRootReservation
		if err := decodeEnvironmentPayload(request.Payload, &reservation); err != nil {
			return environmentOperationResult(nil, err)
		}
		return environmentOperationResult(struct{}{}, s.physical.confirmRoot(reservation))
	case "physical.finish_begin":
		var input struct {
			AbortReason   string                      `json:"abort_reason,omitempty"`
			PreparationID string                      `json:"preparation_id"`
			Participant   execenv.PhysicalParticipant `json:"participant"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}

		permit, err := s.physical.beginFinish(ctx, input.PreparationID, input.Participant, input.AbortReason)
		return environmentOperationResult(permit, err)
	case "physical_finish_confirm":
		var input struct {
			PreparationID string                       `json:"preparation_id"`
			Permit        execenv.PhysicalFinishPermit `json:"permit"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		outcome, err := s.physical.confirmFinish(input.PreparationID, input.Permit)
		return environmentOperationResult(outcome, err)
	case "physical.verify_completion":
		var input struct {
			PreparationID string `json:"preparation_id"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		return environmentOperationResult(struct{}{}, s.physical.verifyCompletionGeneration(input.PreparationID))
	case "cache.coauthor":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
			Enabled     bool   `json:"enabled"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		s.daemon.mu.Lock()
		owned := s.daemon.workspaces[input.WorkspaceID] != nil
		s.daemon.mu.Unlock()
		if !owned {
			return environmentOperationResult(nil, errors.New("workspace is not bound"))
		}
		s.daemon.publishCoAuthoredByState(input.WorkspaceID, func(string) bool { return input.Enabled })
		return environmentOperationResult(struct{}{}, nil)
	case "cache.sync":
		var input struct {
			WorkspaceID string               `json:"workspace_id"`
			Repos       []repocache.RepoInfo `json:"repos"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		for _, repo := range input.Repos {
			if !s.allowsRepo(input.WorkspaceID, repo.URL) {
				return environmentOperationResult(nil, errors.New("repository is outside workspace authorization"))
			}
		}
		return environmentOperationResult(struct{}{}, s.daemon.repoCache.SyncContext(ctx, input.WorkspaceID, input.Repos))
	case "cache.checkout":
		var input repocache.WorktreeParams
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		if !s.allowsRepo(input.WorkspaceID, input.RepoURL) {
			return environmentOperationResult(nil, errors.New("repository is outside workspace authorization"))
		}
		relative, err := filepath.Rel(s.daemon.cfg.WorkspacesRoot, input.WorkDir)
		if err != nil || !filepath.IsLocal(relative) {
			return environmentOperationResult(nil, errors.New("checkout is outside managed storage"))
		}
		result, err := s.daemon.repoCache.CreateWorktreeContext(ctx, input)
		return environmentOperationResult(result, err)
	case "application.source":
		var input environmentApplicationSource
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		if !s.daemon.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID}) {
			return environmentOperationResult(nil, errors.New("application runtime is not owned"))
		}
		config := protocol.DefaultApplicationConfig()
		config.Mode = "managed"
		config.ResourceID = input.ResourceID
		config.Ref = input.Ref
		config.WorkDir = input.WorkDir
		config.Command = []string{"source-validation"}
		config.Health.Kind = "none"
		command := protocol.ApplicationControlCommand{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID, ApplicationID: input.ApplicationID, InstanceID: input.InstanceID, Revision: input.Revision, ResourceType: input.ResourceType, ResourceRef: input.ResourceRef, Config: config}
		var result applicationPreparedSource
		var err error
		result.Root, result.WorkDir, result.Version, result.Dirty, err = s.daemon.applicationSource(ctx, command)
		return environmentOperationResult(result, err)
	case "environment.command":
		var input struct {
			WorkspaceID string                      `json:"workspace_id"`
			RuntimeID   string                      `json:"runtime_id"`
			Command     protocol.EnvironmentCommand `json:"command"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		result, err := s.daemon.executeEnvironmentCommand(ctx, environmentOperationScope{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID}, input.Command)
		return environmentOperationResult(result, err)
	// environment.operation runs the actual physical mutation/restore in the
	// service child so the control parent holds no Git or worktree writer. The
	// parent routes here from runScopedEnvironmentMutation / runScopedEnvironmentRestore
	// (double-gated); this child is the service owner, so those helpers run locally
	// and never re-route. The selection path is re-resolved here so the parent holds
	// no resolved path, and a transport failure is uncertain (never replayed locally).
	case "environment.operation":
		var input environmentOperationInput
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		scope := environmentOperationScope{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID, Automatic: input.Automatic}
		if !s.daemon.environmentRuntimeOwnedHere(scope) {
			return environmentOperationResult(nil, errors.New("operation runtime is not owned"))
		}
		if input.Action == "restore" {
			result, err := s.daemon.runScopedEnvironmentRestore(ctx, scope, input.ArchiveID)
			return environmentOperationResult(result, err)
		}
		known, err := s.daemon.scopedEnvironmentPaths(ctx, scope)
		if err != nil {
			return environmentOperationResult(nil, err)
		}
		path, exists := known[input.Selection.EnvironmentID]
		if !exists {
			return environmentOperationResult(map[string]any{"environment_id": input.Selection.EnvironmentID, "reason": "unowned"}, nil)
		}
		result, err := s.daemon.runScopedEnvironmentMutation(ctx, scope, protocol.EnvironmentOperationRequest{ID: input.OperationID, Action: input.Action}, input.Selection, path)
		return environmentOperationResult(result, err)
	case "review.run":
		var input protocol.LocalReviewCommand
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		if !s.daemon.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID}) {
			return environmentOperationResult(nil, errors.New("review runtime is not owned"))
		}
		// A large review result is written to the private artifact namespace and
		// referenced instead of exceeding the 256 KiB control channel. The exact
		// result shape is preserved for both in-band and artifact paths.
		return s.reviewRunResult(ctx, request, input)
	case "environment.gc":
		var input struct{}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		if s.daemon.cfg.GCEnabled {
			s.daemon.runGC(ctx)
		}
		return environmentOperationResult(struct{}{}, nil)
	case "worktree-delivery.ack":
		var input execenv.SharedWorktreeDelivery
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		return environmentOperationResult(struct{}{}, execenv.AcknowledgeSharedWorktreeDeliveryAt(ctx, input.FilePath, input))
	default:
		return environmentOperationResult(nil, errors.New("unsupported environment mutation"))
	}
}
func (s *environmentProcessService) read(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.physical == nil {
		return environmentOperationResult(nil, errors.New("environment service is not bound"))
	}
	if request.Operation == "environment.inventory" {
		return environmentOperationResult(struct {
			PID  int    `json:"pid"`
			Root string `json:"root"`
		}{os.Getpid(), s.daemon.cfg.WorkspacesRoot}, nil)
	}
	if request.Operation == "worktree-delivery.receipts" {
		var input struct {
			Namespace string `json:"namespace"`
		}
		if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
			return environmentOperationResult(nil, err)
		}
		receipts, err := execenv.PendingSharedWorktreeDeliveries(ctx, input.Namespace)
		if err != nil {
			return environmentOperationResult(nil, err)
		}
		return environmentOperationResult(receipts, nil)
	}
	var input struct {
		WorkspaceID string `json:"workspace_id"`
		URL         string `json:"url"`
	}
	if request.Operation != "cache.lookup" {
		return environmentOperationResult(nil, errors.New("unsupported environment read"))
	}
	if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
		return environmentOperationResult(nil, err)
	}
	if !s.allowsRepo(input.WorkspaceID, input.URL) {
		return environmentOperationResult(nil, errors.New("repository is outside workspace authorization"))
	}
	return environmentOperationResult(struct {
		Path     string `json:"path"`
		BarePath string `json:"bare_path"`
	}{s.daemon.repoCache.Lookup(input.WorkspaceID, input.URL), s.daemon.repoCache.BarePath(input.WorkspaceID, input.URL)}, nil)
}
func (s *environmentProcessService) shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
	if s.physical != nil {
		s.physical.close()
	}
	s.daemon.stopEnvironmentOperations()
	if cache, ok := s.daemon.repoCache.(*repocache.Cache); ok {
		cache.CancelMaintenance()
	}
	if s.factsTransport != nil {
		s.factsTransport.CloseIdleConnections()
	}
	return ctx.Err()
}
