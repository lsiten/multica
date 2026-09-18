package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type VscreenTakeoverSmokeConfig struct {
	NativeExecutable, NativeBuild, EvidenceDir, PrivateRoot string
	Fixture                                                 *smokefixture.App `json:"-"`
}
type VscreenTakeoverPlacement struct {
	Stage        string                    `json:"stage"`
	DisplayID    uint32                    `json:"display_id"`
	Bounds       smokefixture.WindowBounds `json:"bounds"`
	PID          int                       `json:"pid"`
	WindowID     uint32                    `json:"window_id"`
	ProcessStart string                    `json:"process_start"`
	HumanStage   uint64                    `json:"human_stage"`
	Text         string                    `json:"text"`
}
type VscreenTakeoverImage struct {
	PixelSHA256 string `json:"pixel_sha256"`
	Artifact    string `json:"artifact"`
	SHA256      string `json:"sha256"`
}
type VscreenTakeoverSmokeEvidence struct {
	PhysicalSource          *native.SourceDescriptor   `json:"physical_source,omitempty"`
	Scope                   string                     `json:"scope"`
	Error                   string                     `json:"error,omitempty"`
	GUIExercised            bool                       `json:"gui_exercised"`
	FixtureBundleID         string                     `json:"fixture_bundle_id"`
	FixtureBinarySHA256     string                     `json:"fixture_binary_sha256"`
	SourceTaskID            string                     `json:"source_task_id"`
	ContinuationTaskID      string                     `json:"continuation_task_id"`
	InterventionID          string                     `json:"intervention_id"`
	ReturnReceiptID         string                     `json:"return_receipt_id"`
	ProviderStopped         bool                       `json:"provider_stopped"`
	TranscriptDrained       bool                       `json:"transcript_drained"`
	TerminalReported        bool                       `json:"terminal_reported"`
	StoppedAck              bool                       `json:"stopped_ack"`
	HumanAck                bool                       `json:"human_ack"`
	ReturnAck               bool                       `json:"return_ack"`
	FreshObserveBeforeInput bool                       `json:"fresh_observe_before_input"`
	OldLeaseRefused         bool                       `json:"old_lease_refused"`
	OldActionRefused        bool                       `json:"old_action_refused"`
	ContinuationCompleted   bool                       `json:"continuation_completed"`
	CleanupAck              bool                       `json:"cleanup_ack"`
	FixtureClosed           bool                       `json:"fixture_closed"`
	Disposed                bool                       `json:"disposed"`
	HostClosed              bool                       `json:"host_closed"`
	Display                 *native.Display            `json:"display,omitempty"`
	Source                  *native.SourceDescriptor   `json:"source,omitempty"`
	Epoch                   protocol.VscreenEpoch      `json:"epoch"`
	Placements              []VscreenTakeoverPlacement `json:"placements"`
	Images                  []VscreenTakeoverImage     `json:"images"`
	Stages                  []string                   `json:"stages"`
}

// RunVscreenTakeoverSmoke exercises production daemon/provider and local handoff
// methods against a labeled loopback backend. It is not DB or Desktop UI acceptance.
func RunVscreenTakeoverSmoke(ctx context.Context, c VscreenTakeoverSmokeConfig) (e VscreenTakeoverSmokeEvidence, runErr error) {
	e.Scope = "owned-fixture-scripted-local-owner-loopback-backend-not-db-ui"
	if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
		return e, errors.New("gui_not_authorized")
	}
	home, err := os.UserHomeDir()
	if err != nil || c.PrivateRoot == "" || home != filepath.Join(c.PrivateRoot, "home") {
		return e, errors.New("private_smoke_home_required")
	}
	if !native.Supported() {
		return e, errors.New("native_unsupported")
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	app := c.Fixture
	if app == nil {
		app, err = smokefixture.Prepare(c.NativeExecutable, c.EvidenceDir)
		if err != nil {
			return e, err
		}
	}
	return runTakeoverSmokeCore(ctx, c, app, app.BundleID, app.BinarySHA256)
}

type takeoverOwnedFixture interface {
	BeforeLaunch()
	Read() (smokefixture.State, error)
	MarkHumanStage() error
	Stop(context.Context) error
}

func runTakeoverSmokeCore(ctx context.Context, c VscreenTakeoverSmokeConfig, app takeoverOwnedFixture, bundleID, binaryHash string) (e VscreenTakeoverSmokeEvidence, runErr error) {
	e.Scope = "owned-fixture-scripted-local-owner-loopback-backend-not-db-ui"
	stopAttempted := false
	defer func() {
		if !stopAttempted {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := app.Stop(cleanup)
			e.FixtureClosed = err == nil
			runErr = errors.Join(runErr, err)
		}
	}()
	e.FixtureBundleID = bundleID
	e.FixtureBinarySHA256 = binaryHash
	var err error
	nonce := os.Getenv(smokePrivateNonceEnv)
	if len(nonce) < 32 {
		return e, errors.New("private_smoke_nonce_required")
	}
	backend, err := newTakeoverSmokeBackend(nonce)
	if err != nil {
		return e, err
	}
	defer backend.Close()
	wsID, rtID, agentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	e.SourceTaskID = uuid.NewString()
	e.ContinuationTaskID = uuid.NewString()
	backend.workspaceID, backend.runtimeID, backend.agentID, backend.sourceID, backend.continuationID = wsID, rtID, agentID, e.SourceTaskID, e.ContinuationTaskID
	d := &Daemon{cfg: Config{ServerBaseURL: backend.URL, DaemonID: uuid.NewString(), NativeHostExecutable: c.NativeExecutable, NativeHostBuild: c.NativeBuild, NativeVscreenPreferencesPath: filepath.Join(c.PrivateRoot, "enabled.json"), WorkspacesRoot: filepath.Join(c.PrivateRoot, "workspaces"), AgentTimeout: 20 * time.Second, Agents: map[string]AgentEntry{}}, client: NewClient(backend.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workspaces: map[string]*workspaceState{wsID: {runtimeIDs: []string{rtID}}}, runtimeIndex: map[string]Runtime{rtID: {ID: rtID, Provider: "claude", ProfileID: "smoke-provider"}}, profileLaunchSpecs: map[string]profileLaunchSpec{}, activeEnvRoots: map[string]int{}}
	d.client.SetToken(nonce)
	backend.daemon = d
	s := d.vscreenRuntime()
	key, err := d.vscreenResource(wsID, rtID)
	if err != nil {
		return e, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if s.client != nil {
			if backend.connected() {
				cleanupErr := backend.disable(cleanup, d)
				e.CleanupAck = cleanupErr == nil
				if cleanupErr != nil {
					runErr = errors.Join(runErr, cleanupErr)
				}
			}
			disposeErr := d.removeVscreenRuntime(cleanup, s, rtID)
			if disposeErr == nil {
				listed, listErr := s.client.Call(cleanup, native.Request{Operation: "list"})
				disposeErr = listErr
				if listErr == nil {
					e.Disposed = true
					for _, display := range listed.Displays {
						if e.Display != nil && display.ID == e.Display.ID {
							e.Disposed = false
						}
					}
				}
			}
			runErr = errors.Join(runErr, disposeErr)
		}
		stopAttempted = true
		stopErr := app.Stop(cleanup)
		e.FixtureClosed = stopErr == nil
		runErr = errors.Join(runErr, stopErr)
		backend.mu.Lock()
		e.Stages = append([]string(nil), backend.stages...)
		backend.mu.Unlock()
		backend.closeLink()
		d.closeVscreenReporter()
		if s.runCancel != nil {
			s.runCancel()
			<-s.runDone
		}
		if s.client != nil {
			closeErr := s.client.Close()
			e.HostClosed = closeErr == nil
			runErr = errors.Join(runErr, closeErr)
		}
		if runErr == nil && (!e.Disposed || !e.HostClosed || !e.FixtureClosed || !e.CleanupAck) {
			runErr = errors.New("takeover_cleanup_unconfirmed")
		}
	}()
	s.mu.Lock()
	err = d.startVscreenHost(ctx, s)
	s.enabled[key] = err == nil
	s.mu.Unlock()
	if err != nil {
		return e, err
	}
	e.GUIExercised = true
	actor, err := s.manager.For(key)
	if err != nil {
		return e, err
	}
	display, err := actor.Ensure(ctx)
	if err != nil {
		return e, err
	}
	e.Epoch = display.Epoch
	described, err := s.client.Call(ctx, native.Request{Operation: "describe", Resource: key, Epoch: display.Epoch})
	if err != nil {
		return e, err
	}
	e.Display = described.Display
	if e.Display == nil || !e.Display.Managed || e.Display.ID != display.DisplayID {
		return e, errors.New("takeover_display_readback_invalid")
	}
	sources, err := s.client.Sources(ctx, key)
	if err != nil {
		return e, err
	}
	var physical *native.SourceDescriptor
	for i := range sources {
		source := sources[i]
		if source.DisplayID == display.DisplayID {
			e.Source = &source
		}
		if source.Source.Kind == protocol.MirrorSourcePhysical && (physical == nil || source.Primary) {
			physical = &source
		}
	}
	e.PhysicalSource = physical
	if e.Source == nil || physical == nil {
		return e, errors.New("takeover_source_missing")
	}
	if err = backend.connect(ctx, d); err != nil {
		return e, err
	}
	config := smokeProviderConfig{GUIAuthorized: os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") == "1", Nonce: nonce, OwnerPID: os.Getpid(), TaskID: e.SourceTaskID, WorkspaceID: wsID, RuntimeID: rtID, BundleID: bundleID, Stage: "source", Endpoint: backend.URL, EvidenceDir: c.EvidenceDir}
	task := Task{ID: e.SourceTaskID, WorkspaceID: wsID, RuntimeID: rtID, AgentID: agentID, IssueID: uuid.NewString(), AuthToken: "mat_" + nonce + "_" + e.SourceTaskID, Agent: &AgentData{ID: agentID, Name: "Owned smoke provider", McpConfig: json.RawMessage(`{"mcpServers":{}}`), CustomEnv: map[string]string{smokeProviderNonceEnv: nonce}}}
	app.BeforeLaunch()
	source, err := runTakeoverPreparedTask(ctx, d, c, config, task)
	if err != nil {
		return e, err
	}
	if source.Status != "blocked" || source.FailureReason != protocol.VscreenPauseReasonHumanIntervention || source.SessionID == "" {
		return e, fmt.Errorf("source_provider_not_stopped: %s %s", source.Status, source.FailureReason)
	}
	d.reportTaskResult(ctx, task.ID, source, d.logger)
	if err = backend.waitAck(ctx, protocol.VscreenInterventionAwaitingTakeover); err != nil {
		return e, err
	}
	backend.mu.Lock()
	e.ProviderStopped = backend.providerStopped
	e.TranscriptDrained = backend.transcriptDrained
	e.TerminalReported = backend.terminalReported
	e.Stages = append(e.Stages, backend.stages...)
	sourceObserved := backend.sourceObserved
	backend.mu.Unlock()
	e.StoppedAck = true
	s.interventions.mu.Lock()
	record := *s.interventions.records[rtID]
	s.interventions.mu.Unlock()
	e.InterventionID = record.Report.InterventionID
	if len(record.Windows) != 1 || !e.ProviderStopped || !e.TranscriptDrained || !e.TerminalReported {
		return e, errors.New("source_lifecycle_incomplete")
	}
	state, err := awaitTakeoverPlacement(ctx, app, *e.Source, 0, "")
	if err != nil {
		return e, err
	}
	e.Placements = append(e.Placements, takeoverPlacement("source", state))
	if state.Keys != 0 {
		return e, errors.New("unsupported_input_delivered")
	}
	owner, err := randomBrokerToken()
	if err != nil {
		return e, err
	}
	d.SetVscreenLocalOwnerVerifier(func(_ context.Context, value string) bool {
		return subtle.ConstantTimeCompare([]byte(owner), []byte(value)) == 1
	})
	if err = d.TakeOverVscreenLocally(ctx, owner, wsID, rtID, e.InterventionID, physical.Source.SourceID); err != nil {
		return e, err
	}
	if err = backend.waitAck(ctx, protocol.VscreenInterventionHuman); err != nil {
		return e, err
	}
	e.HumanAck = true
	if _, err = awaitTakeoverPlacement(ctx, app, *physical, 0, ""); err != nil {
		return e, err
	}
	if err = app.MarkHumanStage(); err != nil {
		return e, err
	}
	state, err = awaitTakeoverPlacement(ctx, app, *physical, 1, "Multica scripted human handoff")
	if err != nil {
		return e, err
	}
	e.Placements = append(e.Placements, takeoverPlacement("human", state))
	if err = d.ReturnVscreenLocally(ctx, owner, wsID, rtID, e.InterventionID, "Scripted owned fixture handoff; not manual UI acceptance."); err != nil {
		return e, err
	}
	if err = backend.waitAck(ctx, protocol.VscreenInterventionReadyToContinue); err != nil {
		return e, err
	}
	e.ReturnAck = true
	state, err = awaitTakeoverPlacement(ctx, app, *e.Source, 1, "Multica scripted human handoff")
	if err != nil {
		return e, err
	}
	e.Placements = append(e.Placements, takeoverPlacement("return", state))
	observer := appcontrol.Authority{Resource: key, Epoch: e.Epoch}
	observer.ObserverGrant, err = randomBrokerToken()
	if err != nil {
		return e, err
	}
	if err = s.client.GrantObserver(ctx, observer, 5*time.Second); err != nil {
		return e, err
	}
	observed, observeErr := s.client.ObserveApp(ctx, observer, record.Windows[0], true)
	revokeErr := s.client.RevokeObserver(ctx, observer)
	if observeErr != nil || revokeErr != nil {
		return e, errors.Join(observeErr, revokeErr)
	}
	if observed.Window.Process.PID != state.PID || observed.Window.WindowID != state.WindowID || observed.Window.Process.Start != state.ProcessStart {
		return e, errors.New("return_window_identity_mismatch")
	}
	if err = writeTakeoverPNG(c.EvidenceDir, "takeover-return.png", observed.PNG); err != nil {
		return e, err
	}
	e.OldLeaseRefused = smokeNativeRefused(s.client.Renew(ctx, record.Authority, time.Second))
	oldAction := protocol.VscreenActionRequest{Target: protocol.VscreenActionTarget{Resource: key, TaskID: e.SourceTaskID, TransactionID: record.Authority.TransactionID, LeaseEpoch: record.Authority.LeaseEpoch, Epoch: e.Epoch, WindowHandle: record.Windows[0], SnapshotRevision: sourceObserved.Revision}, ActionID: "smoke-old-action", Sequence: 2, Action: protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: sourceObserved.Element, Text: "old action must never run"}}}
	_, oldErr := s.client.ActApp(ctx, oldAction)
	e.OldActionRefused = smokeNativeRefused(oldErr)
	if !e.OldLeaseRefused || !e.OldActionRefused {
		return e, errors.New("old_native_authority_not_refused")
	}
	s.interventions.mu.Lock()
	returned := s.interventions.records[rtID].Report
	s.interventions.mu.Unlock()
	e.ReturnReceiptID = returned.ReturnReceiptID
	task.ID = e.ContinuationTaskID
	task.AuthToken = "mat_" + nonce + "_" + task.ID
	task.VscreenContinuation = &protocol.VscreenContinuationContext{InterventionID: e.InterventionID, SourceTaskID: e.SourceTaskID, Epoch: e.Epoch, ReturnReceiptID: e.ReturnReceiptID, FreshSession: true, HumanSummary: "Scripted owned fixture handoff"}
	config.Stage = "continuation"
	config.TaskID = task.ID
	continued, err := runTakeoverPreparedTask(ctx, d, c, config, task)
	if err != nil {
		return e, err
	}
	if continued.Status != "completed" {
		return e, errors.New("continuation_not_completed")
	}
	d.reportTaskResult(ctx, task.ID, continued, d.logger)
	e.ContinuationCompleted = true
	backend.mu.Lock()
	e.FreshObserveBeforeInput = backend.freshObserveBeforeInput
	e.Stages = append([]string(nil), backend.stages...)
	backend.mu.Unlock()
	if !e.FreshObserveBeforeInput {
		return e, errors.New("continuation_observation_order_invalid")
	}
	state, err = awaitTakeoverPlacement(ctx, app, *e.Source, 1, "Multica continuation verified")
	if err != nil {
		return e, err
	}
	e.Placements = append(e.Placements, takeoverPlacement("continuation", state))
	for _, name := range []string{"takeover-source.png", "takeover-return.png", "takeover-continuation.png"} {
		raw, err := os.ReadFile(filepath.Join(c.EvidenceDir, name))
		if err != nil {
			return e, err
		}
		sum := sha256.Sum256(raw)
		decoded, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return e, err
		}
		pixels := image.NewNRGBA(decoded.Bounds())
		draw.Draw(pixels, pixels.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
		pixelHash := sha256.Sum256(pixels.Pix)
		e.Images = append(e.Images, VscreenTakeoverImage{Artifact: filepath.Join(c.EvidenceDir, name), SHA256: hex.EncodeToString(sum[:]), PixelSHA256: hex.EncodeToString(pixelHash[:])})
	}
	if e.Images[0].PixelSHA256 == e.Images[1].PixelSHA256 {
		return e, errors.New("human_stage_pixels_unchanged")
	}
	return e, nil
}
func runTakeoverPreparedTask(ctx context.Context, d *Daemon, c VscreenTakeoverSmokeConfig, config smokeProviderConfig, task Task) (TaskResult, error) {
	path := filepath.Join(c.PrivateRoot, config.Stage+"-provider.json")
	if err := writeSmokePrivateJSON(path, config); err != nil {
		return TaskResult{}, err
	}
	d.profileLaunchSpecs["smoke-provider"] = profileLaunchSpec{path: c.NativeExecutable, version: "owned-smoke-fixture", fixedArgs: []string{VscreenSmokeProviderCommand, path}}
	return d.runTask(ctx, task, "claude", 0, d.logger)
}
func smokeNativeRefused(err error) bool {
	var remote *hostclient.RemoteError
	return errors.As(err, &remote)
}
func takeoverPlacement(stage string, s smokefixture.State) VscreenTakeoverPlacement {
	return VscreenTakeoverPlacement{Stage: stage, DisplayID: s.DisplayID, Bounds: s.Bounds, PID: s.PID, WindowID: s.WindowID, ProcessStart: s.ProcessStart, HumanStage: s.HumanStage, Text: s.Text}
}
func awaitTakeoverPlacement(ctx context.Context, app interface {
	Read() (smokefixture.State, error)
}, source native.SourceDescriptor, human uint64, text string) (smokefixture.State, error) {
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		s, err := app.Read()
		if err == nil && !s.Closed && s.PID > 0 && s.WindowID > 0 && s.ProcessStart != "" && s.DisplayID == source.DisplayID && s.Bounds.Width > 0 && s.Bounds.Height > 0 && s.Bounds.X >= float64(source.X) && s.Bounds.Y >= float64(source.Y) && s.Bounds.X+s.Bounds.Width <= float64(source.X)+source.LogicalWidth && s.Bounds.Y+s.Bounds.Height <= float64(source.Y)+source.LogicalHeight && s.HumanStage == human && (text == "" || s.Text == text) {
			return s, nil
		}
		select {
		case <-wait.Done():
			return s, errors.New("fixture_placement_unconfirmed")
		case <-ticker.C:
		}
	}
}
