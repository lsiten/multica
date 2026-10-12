package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const applicationPollInterval = 30 * time.Second

func (d *Daemon) wakeApplications(runtimeIDs ...string) {
	selected := map[string]bool{}
	for _, id := range runtimeIDs {
		selected[id] = true
	}
	if d.applicationProcessMode() {
		if len(runtimeIDs) == 0 {
			d.applicationWakeRuntimes.Store("*", true)
		} else {
			for _, id := range runtimeIDs {
				d.applicationWakeRuntimes.Store(id, true)
			}
		}
	}
	d.applicationPollStates.Range(func(key, value any) bool {
		if len(selected) > 0 && !selected[key.(string)] {
			return true
		}
		state := value.(*applicationPollState)
		state.mu.Lock()
		state.next = time.Time{}
		state.wake++
		state.mu.Unlock()
		return true
	})
	if d.applicationWakeNotify != nil {
		d.applicationWakeNotify(runtimeIDs)
	}
	if d.applicationWake != nil {
		select {
		case d.applicationWake <- struct{}{}:
		default:
		}
	}
}

type applicationPollState struct {
	mu       sync.Mutex
	failures int
	next     time.Time
	polling  bool
	wake     uint64
}

func (d *Daemon) applicationLoop(ctx context.Context) {
	var workers sync.WaitGroup
	defer workers.Wait()
	semaphore := make(chan struct{}, 4)
	ticker := time.NewTicker(applicationPollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		for _, runtimeID := range d.allRuntimeIDs() {
			if _, supported := d.applicationServerCapabilities.Load(runtimeID); supported {
				d.pollRuntimeApplications(ctx, semaphore, &workers, runtimeID)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.applicationWake:
		}
	}
}

func (d *Daemon) pollRuntimeApplications(ctx context.Context, semaphore chan struct{}, workers *sync.WaitGroup, runtimeID string) {
	value, _ := d.applicationPollStates.LoadOrStore(runtimeID, &applicationPollState{})
	state := value.(*applicationPollState)
	state.mu.Lock()
	if state.polling || d.reportClock().Before(state.next) {
		state.mu.Unlock()
		return
	}
	state.polling = true
	wake := state.wake
	state.mu.Unlock()
	success := false
	defer func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.polling = false
		if success {
			state.failures = 0
			if state.wake == wake {
				state.next = d.reportClock().Add(downwardJitterDuration(applicationPollInterval))
			}
		} else if ctx.Err() == nil && state.wake == wake {
			state.failures++
			state.next = d.reportClock().Add(reportRetryDelay(state.failures, nil))
		}
	}()
	instances, err := d.applicationTransportFor(runtimeID).Sync(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.logger.Debug("application registry unavailable", "runtime_id", runtimeID, "error", err)
		}
		return
	}
	d.rememberApplicationRegistry(runtimeID, instances)
	for _, instance := range instances {
		command := instance.Command
		if command.RuntimeID != runtimeID || !d.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: command.WorkspaceID, RuntimeID: runtimeID}) {
			continue
		}
		d.applicationCommands.Store(command.InstanceID, command)
		if local, err := d.readLocalIntent(command.InstanceID); err == nil && local != nil && !local.Synced && local.Generation == command.Generation {
			if local.Result == nil || instance.DesiredState != local.DesiredState {
				continue
			}
		}
		d.rememberApplicationGeneration(command)
		if instance.HasPendingOperation {
			continue
		}
		if instance.ConfirmedStopped {
			continue
		}
		lock := d.applicationInstanceLock(command.InstanceID)
		if !lock.TryLock() {
			continue
		}
		if instance.DesiredState == "stopped" {
			observation, stopErr := d.stopApplication(ctx, command)
			if stopErr != nil {
				observation = applicationObservation(command, "unknown", "unknown", applicationHostError(stopErr, command))
			}
			if observeErr := d.queueApplicationObservation(runtimeID, observation); observeErr != nil && ctx.Err() == nil {
				d.logger.Debug("application stop observation deferred", "instance_id", command.InstanceID, "error", observeErr)
			}
			lock.Unlock()
			continue
		}
		observation, inspectErr := d.inspectApplication(ctx, command)
		lock.Unlock()
		if inspectErr != nil && command.Action == "resume" {
			resumeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			observation, inspectErr = d.executeApplication(resumeCtx, command)
			cancel()
		}
		if inspectErr != nil && instance.CanRestore && command.Config.Mode == "managed" && command.Config.Restart.Restore {
			key := "restore/" + command.InstanceID
			if _, running := d.applicationFlights.LoadOrStore(key, true); !running {
				workers.Go(func() {
					defer d.applicationFlights.Delete(key)
					select {
					case semaphore <- struct{}{}:
						defer func() { <-semaphore }()
					case <-ctx.Done():
						return
					}
					restored, restoreErr := d.executeApplication(ctx, command)
					if restoreErr != nil {
						restored = applicationObservation(command, "unknown", "unknown", applicationHostError(restoreErr, command))
					}
					if err := d.queueApplicationObservation(runtimeID, restored); err != nil && ctx.Err() == nil {
						d.logger.Debug("application restore observation deferred", "instance_id", command.InstanceID, "error", err)
					}
				})
			}
			continue
		}
		if inspectErr != nil {
			observation.Error = applicationHostError(inspectErr, command)
		}
		if err := d.queueApplicationObservation(runtimeID, observation); err != nil && ctx.Err() == nil {
			d.logger.Debug("application observation deferred", "instance_id", command.InstanceID, "error", err)
		}
	}
	claims, err := d.applicationTransportFor(runtimeID).Claim(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.logger.Debug("application claim deferred", "runtime_id", runtimeID, "error", err)
		}
		return
	}
	success = true
	for _, claim := range claims {
		if claim.Command.RuntimeID != runtimeID {
			continue
		}
		if _, running := d.applicationFlights.LoadOrStore(claim.StepID, true); running {
			continue
		}
		d.applicationCommands.Store(claim.Command.InstanceID, claim.Command)
		d.rememberApplicationGeneration(claim.Command)
		workers.Go(func() {
			defer d.applicationFlights.Delete(claim.StepID)
			d.runApplicationClaim(ctx, semaphore, runtimeID, claim)
		})
	}
}

func (d *Daemon) runApplicationClaim(parent context.Context, semaphore chan struct{}, runtimeID string, claim protocol.ApplicationClaim) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		ticker := time.NewTicker(12 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := d.applicationTransportFor(runtimeID).RenewLease(ctx, claim)
				var request *requestError
				if errors.As(err, &request) && slices.Contains([]int{http.StatusConflict, http.StatusNotFound, http.StatusForbidden, http.StatusUnauthorized}, request.StatusCode) {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-leaseDone }()
	select {
	case semaphore <- struct{}{}:
		defer func() { <-semaphore }()
	case <-ctx.Done():
		return
	}
	if err := d.applicationTransportFor(runtimeID).RenewLease(ctx, claim); err != nil {
		return
	}
	if local, err := d.readLocalIntent(claim.Command.InstanceID); err == nil && local != nil && !local.Synced && local.Generation == claim.Command.Generation && claim.Command.Action != "publish" && claim.Command.Action != "unpublish" {
		observation, inspectErr := d.inspectApplication(ctx, claim.Command)
		if inspectErr == nil {
			if err := d.queueApplicationStep(runtimeID, claim, protocol.ApplicationStepResult{ClaimToken: claim.ClaimToken, State: "failed", Error: "superseded by local runtime command", Observation: observation}); err != nil {
				d.logger.Warn("application superseded receipt storage deferred", "error", err)
			}
		}
		return
	}
	if saved, oldItem, err := d.pendingApplicationStep(ctx, runtimeID, claim); err != nil {
		d.logger.Warn("application pending receipt could not be verified", "step_id", claim.StepID, "error", err)
		return
	} else if saved != nil {
		if err := d.queueApplicationStep(runtimeID, claim, *saved); err != nil {
			d.logger.Warn("application recovered receipt storage deferred", "error", err)
		} else if oldItem != nil && oldItem.fileName != "" {
			if err := d.terminalReports.acknowledge(*oldItem); err != nil {
				d.logger.Warn("application stale receipt cleanup deferred", "error", err)
			}
		}
		return
	}
	observation, err := d.executeApplication(ctx, claim.Command)
	if ctx.Err() != nil {
		return
	}
	result := protocol.ApplicationStepResult{ClaimToken: claim.ClaimToken, State: "completed", Observation: observation}
	if err != nil {
		result.State = "failed"
		result.Error = applicationHostError(err, claim.Command)
		result.Observation = applicationObservation(claim.Command, observation.ProcessState, observation.HealthState, result.Error)
	}
	if completeErr := d.queueApplicationStep(runtimeID, claim, result); completeErr != nil {
		d.logger.Warn("application result storage deferred", "step_id", claim.StepID, "error", completeErr)
	}
	d.wakeApplications(runtimeID)
}

func (d *Daemon) applicationRecords() ([]applicationhost.Record, error) {
	root := filepath.Join(d.cfg.WorkspacesRoot, ".applications", d.cfg.DaemonID)
	records := []applicationhost.Record{}
	canonical, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	base, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil || canonical != filepath.Join(base, ".applications", d.cfg.DaemonID) {
		return nil, errors.New("application inventory root is redirected")
	}
	workspaces, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspaces {
		if workspace.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("application workspace inventory is redirected")
		}
		if !workspace.IsDir() {
			continue
		}
		instances, readErr := os.ReadDir(filepath.Join(root, workspace.Name()))
		if readErr != nil {
			return nil, readErr
		}
		for _, instance := range instances {
			if instance.Type()&os.ModeSymlink != 0 {
				return nil, errors.New("application instance inventory is redirected")
			}
			if !instance.IsDir() {
				continue
			}
			record, readErr := applicationhost.ReadRecord(filepath.Join(root, workspace.Name(), instance.Name(), "host.json"))
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return nil, readErr
			}
			if record.Command.WorkspaceID != workspace.Name() || record.Command.InstanceID != instance.Name() {
				return nil, errors.New("application record scope does not match its storage directory")
			}
			if len(records) >= 4096 {
				return nil, errors.New("application inventory exceeds limit")
			}
			records = append(records, record)
		}
	}
	return records, nil
}

func (d *Daemon) stopOwnedApplications() {
	records, err := d.applicationRecords()
	if err != nil {
		d.logger.Warn("application shutdown inventory could not be verified", "error", err)
		return
	}
	for _, record := range records {
		if record.Observation.ProcessState == "stopped" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			directory, err := d.applicationDirectory(record.Command)
			if err == nil {
				err = applicationhost.WaitStopped(ctx, filepath.Join(directory, "host.json"), record.HostID)
			}
			cancel()
			if err != nil {
				d.logger.Warn("application shutdown cleanup is not confirmed", "instance_id", record.Command.InstanceID, "error", err)
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		client, clientErr := applicationhost.NewClient(record)
		if clientErr == nil {
			status, stopErr := client.Stop(ctx, record.Command.Generation)
			if stopErr == nil {
				directory, directoryErr := d.applicationDirectory(record.Command)
				if directoryErr != nil {
					stopErr = directoryErr
				} else {
					stopErr = applicationhost.WaitStopped(ctx, filepath.Join(directory, "host.json"), record.HostID)
				}
			}
			if stopErr == nil {
				if err = d.queueApplicationObservation(record.Command.RuntimeID, status.Observation); err != nil {
					d.logger.Debug("application shutdown report deferred", "instance_id", record.Command.InstanceID, "error", err)
				}
			} else {
				d.logger.Warn("application shutdown was not confirmed", "instance_id", record.Command.InstanceID, "error", stopErr)
			}
		} else {
			d.logger.Warn("application shutdown ownership unavailable", "instance_id", record.Command.InstanceID, "error", clientErr)
		}
		cancel()
	}
}

func (d *Daemon) applicationReferencesDirectory(path string) (bool, error) {
	records, err := d.applicationRecords()
	if err != nil {
		return true, err
	}
	if len(records) == 0 {
		return false, nil
	}
	root, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.Observation.ProcessState == "stopped" {
			recordPath := filepath.Join(d.cfg.WorkspacesRoot, ".applications", d.cfg.DaemonID, record.Command.WorkspaceID, record.Command.InstanceID, "host.json")
			if err := applicationhost.WithStoppedOwnership(recordPath, record.HostID, func(applicationhost.Record) error { return nil }); err != nil {
				return true, err
			}
			continue
		}
		relative, relErr := filepath.Rel(root, record.SourceRoot)
		if relErr == nil && filepath.IsLocal(relative) {
			return true, nil
		}
	}
	return false, nil
}
