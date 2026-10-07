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

func (d *Daemon) wakeApplications() {
	if d.applicationWake != nil {
		select {
		case d.applicationWake <- struct{}{}:
		default:
		}
	}
}

func (d *Daemon) applicationLoop(ctx context.Context) {
	var workers sync.WaitGroup
	defer workers.Wait()
	semaphore := make(chan struct{}, 4)
	ticker := time.NewTicker(5 * time.Second)
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
	instances, err := d.client.syncApplications(ctx, runtimeID, d.cfg.DaemonID)
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
			if observeErr := d.client.observeApplication(ctx, runtimeID, d.cfg.DaemonID, observation); observeErr != nil && ctx.Err() == nil {
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
					if err := d.client.observeApplication(ctx, runtimeID, d.cfg.DaemonID, restored); err != nil && ctx.Err() == nil {
						d.logger.Debug("application restore observation deferred", "instance_id", command.InstanceID, "error", err)
					}
				})
			}
			continue
		}
		if inspectErr != nil {
			observation.Error = applicationHostError(inspectErr, command)
		}
		if err := d.client.observeApplication(ctx, runtimeID, d.cfg.DaemonID, observation); err != nil && ctx.Err() == nil {
			d.logger.Debug("application observation deferred", "instance_id", command.InstanceID, "error", err)
		}
	}
	claims, err := d.client.claimApplications(ctx, runtimeID, d.cfg.DaemonID)
	if err != nil {
		if ctx.Err() == nil {
			d.logger.Debug("application claim deferred", "runtime_id", runtimeID, "error", err)
		}
		return
	}
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
				err := d.client.renewApplicationLease(ctx, runtimeID, d.cfg.DaemonID, claim)
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
	if err := d.client.renewApplicationLease(ctx, runtimeID, d.cfg.DaemonID, claim); err != nil {
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
	if completeErr := d.client.completeApplication(ctx, runtimeID, d.cfg.DaemonID, claim, result); completeErr != nil {
		d.logger.Debug("application result will be reconciled by the next lease", "step_id", claim.StepID, "error", completeErr)
	}
	d.wakeApplications()
}

func (d *Daemon) applicationRecords() ([]applicationhost.Record, error) {
	root := filepath.Join(d.cfg.WorkspacesRoot, ".applications", d.cfg.DaemonID)
	records := []applicationhost.Record{}
	workspaces, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspaces {
		if !workspace.IsDir() || workspace.Type()&os.ModeSymlink != 0 {
			continue
		}
		instances, readErr := os.ReadDir(filepath.Join(root, workspace.Name()))
		if readErr != nil {
			return nil, readErr
		}
		for _, instance := range instances {
			if !instance.IsDir() || instance.Type()&os.ModeSymlink != 0 {
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
				if err = d.client.observeApplication(ctx, record.Command.RuntimeID, d.cfg.DaemonID, status.Observation); err != nil {
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
			continue
		}
		relative, relErr := filepath.Rel(root, record.SourceRoot)
		if relErr == nil && filepath.IsLocal(relative) {
			return true, nil
		}
	}
	return false, nil
}
