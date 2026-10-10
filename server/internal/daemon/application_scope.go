package daemon

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
)

type applicationExecution struct {
	workspaceID string
	runtimeID   string
	cancel      context.CancelFunc
}

func lockApplicationUntil(ctx context.Context, lock *sync.Mutex) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for !lock.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

func (d *Daemon) stopUnavailableWorkspaceApplications(ctx context.Context, available map[string]string) {
	if d.applicationProcessMode() {
		d.applicationProcessMu.Lock()
		client := d.applicationProcess
		d.applicationProcessMu.Unlock()
		if client != nil {
			client.mu.Lock()
			removed := []string{}
			for id, grant := range client.grants {
				if _, ok := available[grant.WorkspaceID]; !ok {
					removed = append(removed, id)
				}
			}
			client.mu.Unlock()
			for _, id := range removed {
				if err := client.remove(ctx, id); err != nil {
					d.logger.Warn("application workspace shutdown unconfirmed", "error", err)
				}
			}
		}
		return
	}

	d.stopApplicationScopes(ctx, func(workspaceID, _ string) bool {
		_, allowed := available[workspaceID]
		return !allowed
	})
}

func (d *Daemon) stopRuntimeApplications(ctx context.Context, runtimeID string) {
	if d.applicationProcessMode() {
		d.applicationProcessMu.Lock()
		client := d.applicationProcess
		d.applicationProcessMu.Unlock()
		if client != nil {
			if err := client.remove(ctx, runtimeID); err != nil {
				d.logger.Warn("application runtime shutdown unconfirmed", "error", err)
			}
		}
		return
	}

	d.stopApplicationScopes(ctx, func(_ string, currentRuntimeID string) bool {
		return currentRuntimeID == runtimeID
	})
}

func (d *Daemon) stopApplicationScopes(ctx context.Context, revoked func(string, string) bool) {
	stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var pending []string
	d.applicationExecutions.Range(func(key, value any) bool {
		execution := value.(applicationExecution)
		if revoked(execution.workspaceID, execution.runtimeID) {
			execution.cancel()
			pending = append(pending, key.(string))
		}
		return true
	})
	// Join cancelled preparation before inventorying records it may have created.
	for _, instanceID := range pending {
		lock := d.applicationInstanceLock(instanceID)
		if err := lockApplicationUntil(stopCtx, lock); err != nil {
			d.logger.Warn("revoked application preparation has not settled", "instance_id", instanceID)
			return
		}
		lock.Unlock()
	}
	records, err := d.applicationRecords()
	if err != nil {
		d.logger.Warn("revoked application inventory could not be verified", "error", err)
		return
	}
	for _, record := range records {
		if !revoked(record.Command.WorkspaceID, record.Command.RuntimeID) || record.Observation.ProcessState == "stopped" {
			continue
		}
		lock := d.applicationInstanceLock(record.Command.InstanceID)
		if err := lockApplicationUntil(stopCtx, lock); err != nil {
			return
		}
		err := d.stopRevokedApplication(stopCtx, record)
		lock.Unlock()
		if err != nil {
			d.logger.Warn("revoked application shutdown has not been confirmed", "instance_id", record.Command.InstanceID, "error", err)
		}
	}
}

func (d *Daemon) stopRevokedApplication(ctx context.Context, record applicationhost.Record) error {
	path, _, err := d.applicationRecord(record.Command)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := applicationhost.ReadRecord(path)
		if err != nil || current.HostID != record.HostID {
			return errors.New("application shutdown ownership changed")
		}
		if current.Observation.ProcessState == "stopped" {
			return applicationhost.WaitStopped(ctx, path, current.HostID)
		}
		client, err := applicationhost.NewClient(current)
		if err == nil {
			if _, err = client.Stop(ctx, current.Command.Generation); err == nil {
				return applicationhost.WaitStopped(ctx, path, current.HostID)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
