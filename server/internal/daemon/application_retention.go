package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const applicationStorageRetention = 7 * 24 * time.Hour

type applicationRegistry struct {
	observedAt time.Time
	instances  map[string]protocol.ApplicationRuntimeInstance
}

func (d *Daemon) rememberApplicationRegistry(runtimeID string, instances []protocol.ApplicationRuntimeInstance) {
	snapshot := applicationRegistry{observedAt: time.Now(), instances: make(map[string]protocol.ApplicationRuntimeInstance, len(instances))}
	for _, instance := range instances {
		snapshot.instances[instance.Command.InstanceID] = instance
	}
	d.applicationRegistries.Store(runtimeID, snapshot)
}

func (d *Daemon) gcApplicationStorage(ctx context.Context, now time.Time) {
	if d.applicationProcessMode() {
		return
	}
	gcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx = gcCtx
	records, err := d.applicationRecords()
	if err != nil {
		d.logger.Debug("application retention inventory unavailable", "error", err)
		return
	}
	for _, record := range records {
		if ctx.Err() != nil {
			return
		}
		if record.Observation.ProcessState != "stopped" {
			continue
		}
		stored, ok := d.applicationRegistries.Load(record.Command.RuntimeID)
		if !ok {
			continue
		}
		registry := stored.(applicationRegistry)
		if now.Sub(registry.observedAt) > 3*time.Minute {
			continue
		}
		if instance, exists := registry.instances[record.Command.InstanceID]; exists && (!instance.ConfirmedStopped || instance.HasPendingOperation || instance.Command.Generation < record.Command.Generation) {
			continue
		}
		lock := d.applicationInstanceLock(record.Command.InstanceID)
		if !lock.TryLock() {
			continue
		}
		err := d.pruneStoppedApplicationStorage(ctx, record, now)
		lock.Unlock()
		if err != nil {
			d.logger.Debug("application retained storage cleanup deferred", "instance_id", record.Command.InstanceID, "error", err)
		}
	}
}

func (d *Daemon) pruneStoppedApplicationStorage(ctx context.Context, record applicationhost.Record, now time.Time) error {
	directory, err := d.applicationDirectory(record.Command)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	base, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	expected := filepath.Join(base, ".applications", d.cfg.DaemonID, record.Command.WorkspaceID, record.Command.InstanceID)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(expected) {
		return errors.New("application storage path is not an owned directory")
	}
	directory = canonical
	path := filepath.Join(directory, "host.json")
	info, err := os.Stat(path)
	if err != nil || now.Sub(info.ModTime()) < applicationStorageRetention {
		return err
	}
	return applicationhost.WithStoppedOwnership(path, record.HostID, func(current applicationhost.Record) error {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if name != "service.log" && name != "service.log.1" && name != "service.log.2" && name != "host-start.log" && !strings.HasPrefix(name, ".host-") {
				continue
			}
			if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		// The stopped receipt retains generation fencing; original project directories are never reclaimed.
		if current.Command.ResourceType == "local_directory" {
			return nil
		}
		revisions := filepath.Join(directory, "revisions")
		checkouts, err := os.ReadDir(revisions)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, checkout := range checkouts {
			if !checkout.IsDir() || checkout.Type()&os.ModeSymlink != 0 {
				continue
			}
			root := filepath.Join(revisions, checkout.Name())
			if _, err := execenv.PruneUnusedSharedDirectory(ctx, root); err != nil {
				return err
			}
		}
		return nil
	})
}
