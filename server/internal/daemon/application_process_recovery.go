package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func (d *Daemon) reconcileApplicationManager(ctx context.Context, root string, scope runtimeproc.Scope) error {
	previous, err := runtimeproc.InspectRecord(root, scope)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Host processes have independent ownership. Recovery proves their exact
	// authenticated inventory while the manager lock is held, never by PID.
	return runtimeproc.Reconcile(ctx, root, previous.Identity, func(ctx context.Context, _ runtimeproc.Record) (runtimeproc.Reconciliation, error) {
		lock, err := lockMirrorProcessDomain(filepath.Join(root, "application-manager.lock"))
		if err != nil {
			return runtimeproc.Reconciliation{}, err
		}
		fail := func(err error) (runtimeproc.Reconciliation, error) {
			lock.Close()
			return runtimeproc.Reconciliation{}, err
		}
		records, err := d.applicationRecords()
		if err != nil {
			return fail(err)
		}
		inventory := make([]map[string]string, 0, len(records))
		for _, record := range records {
			path, _, err := d.applicationRecord(record.Command)
			if err != nil {
				return fail(err)
			}
			if record.Observation.ProcessState == "stopped" {
				if err = applicationhost.WaitStopped(ctx, path, record.HostID); err != nil {
					return fail(err)
				}
			} else {
				client, err := applicationhost.NewClient(record)
				if err != nil {
					return fail(err)
				}
				if _, err = client.Status(ctx); err != nil {
					return fail(err)
				}
			}
			inventory = append(inventory, map[string]string{"instance_id": record.Command.InstanceID, "host_id": record.HostID, "state": record.Observation.ProcessState})
		}
		return runtimeproc.Reconciliation{Inventory: marshalRaw(inventory), Release: lock.Close}, nil
	})
}
