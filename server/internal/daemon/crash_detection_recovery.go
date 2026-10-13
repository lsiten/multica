package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// crashDetectAndReconcile retires a non-stopped prior owner that is provably
// dead, so a crashed service's record does not permanently block the split
// process (which would otherwise stay failed-closed to in-process forever).
//
// It never retires a live owner. Safety is layered:
//   - a liveness pre-check avoids attempting Reconcile when the prior owner is
//     reachable (Tier 1: the prior instance's own loopback control endpoint);
//   - runtimeproc.Reconcile's owner.lock is the final gate: a live prior owner
//     holds it for its whole lifetime, so Reconcile cannot acquire it and fails
//     instead of retiring a live owner, even if the liveness pre-check is wrong.
//
// Returns nil if the prior owner was retired (or there is no prior record / it
// is already replaceable), or a non-nil error to fail closed to the legacy
// in-process owner when the prior owner is live or its liveness is uncertain.
func (d *Daemon) crashDetectAndReconcile(ctx context.Context, root string, scope runtimeproc.Scope, service string) error {
	previous, err := runtimeproc.InspectRecord(root, scope)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// A clean stopped record with all operations completed is already
	// replaceable: runtimeproc.Start would have succeeded, so this is not the
	// crash case and there is nothing to reconcile.
	if previous.State == "stopped" {
		return nil
	}
	// The prior owner is non-stopped. Never retire it while it is reachable:
	// a reachable prior owner is live (or was just confirmed live) and must
	// keep its record. Only an unreachable prior owner may be retired.
	if alive, aerr := runtimeprocLive(ctx, root, previous.Identity); aerr == nil && alive {
		return errors.New("runtime prior owner is live")
	}
	// Unreachable prior owner: attempt to retire it. runtimeproc.Reconcile
	// re-checks via the owner.lock, which a live owner still holds, so a wrong
	// liveness pre-check cannot retire a live owner.
	return runtimeproc.Reconcile(ctx, root, previous.Identity, func(ctx context.Context, _ runtimeproc.Record) (runtimeproc.Reconciliation, error) {
		// Re-verify liveness while holding the reconcile domain lock so a prior
		// owner that came back online cannot be retired mid-reconcile. A live
		// owner still holds the owner.lock, so Reconcile cannot reach here, but
		// this keeps the domain proof real and not a no-op.
		if alive, aerr := runtimeprocLive(ctx, root, previous.Identity); aerr == nil && alive {
			return runtimeproc.Reconciliation{}, errors.New("runtime prior owner became live during reconciliation")
		}
		lock, err := lockMirrorProcessDomain(filepath.Join(root, "reconcile.lock"))
		if err != nil {
			return runtimeproc.Reconciliation{}, err
		}
		inventory, err := json.Marshal(map[string]string{
			"service":     service,
			"instance_id": previous.Identity.InstanceID,
			"reason":      "unreachable_prior_owner",
		})
		if err != nil {
			lock.Close()
			return runtimeproc.Reconciliation{}, err
		}
		return runtimeproc.Reconciliation{Inventory: inventory, Release: lock.Close}, nil
	})
}

// runtimeprocLive reports whether the prior owner named by identity is reachable
// on its own loopback control endpoint. A reachable owner is live; an
// unreachable owner may be dead, but reachability is only a pre-check: the
// owner.lock held by runtimeproc.Reconcile is the final authority.
func runtimeprocLive(ctx context.Context, root string, identity runtimeproc.Identity) (bool, error) {
	// Open authenticates readiness against the prior owner's own record/token;
	// a reachable owner answers, an unreachable one cannot.
	if _, err := runtimeproc.Open(ctx, root, identity); err != nil {
		return false, nil
	}
	return true, nil
}
