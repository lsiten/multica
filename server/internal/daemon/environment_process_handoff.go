package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

type environmentHandoffRecovery struct {
	Reservation execenv.PhysicalRootReservation `json:"reservation"`
}

type environmentHandoffResult struct {
	Settled bool `json:"settled"`
}

// environmentAllocationRejected is produced only before preparation effects begin.
type environmentAllocationRejected struct{ Cause error }

func (e *environmentAllocationRejected) Error() string { return e.Cause.Error() }
func (e *environmentAllocationRejected) Unwrap() error { return e.Cause }

// recoverUnclaimed settles only an unused allocation whose caller lost its
// handle. It preserves all checkout content and proves root ownership anew.
func (o *environmentPhysicalOwner) recoverUnclaimed(ctx context.Context, input environmentHandoffRecovery) (environmentHandoffResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	preparation := o.preparations[input.Reservation.ID]
	if preparation == nil || preparation.Result.Reservation != input.Reservation {
		return environmentHandoffResult{}, errors.New("physical handoff identity is unknown")
	}
	raw, err := os.ReadFile(filepath.Join(o.journal, input.Reservation.ID+".json"))
	if err != nil {
		return environmentHandoffResult{}, err
	}
	var durable environmentPhysicalPreparation
	if json.Unmarshal(raw, &durable) != nil || durable.Result.Reservation != input.Reservation {
		return environmentHandoffResult{}, errors.New("physical handoff journal is unconfirmed")
	}
	if durable.Outcome != nil {
		return environmentHandoffResult{Settled: true}, nil
	}
	if preparation.Outcome != nil || preparation.RootConfirmed || preparation.Selected || preparation.Attached || preparation.Participant != nil || preparation.finish != nil || preparation.bridge != nil {
		return environmentHandoffResult{}, nil
	}
	if err := ctx.Err(); err != nil {
		return environmentHandoffResult{}, err
	}
	relative, err := filepath.Rel(o.root, input.Reservation.RootDir)
	if err != nil || !filepath.IsLocal(relative) {
		return environmentHandoffResult{}, errors.New("physical handoff escaped workspace storage")
	}
	root, err := os.OpenRoot(o.root)
	if err != nil {
		return environmentHandoffResult{}, err
	}
	defer root.Close()
	claim, _, err := execenv.LockEnvRootForReuse(root, relative, input.Reservation.RootDir)
	if err != nil {
		return environmentHandoffResult{}, err
	}
	if claim == nil {
		return environmentHandoffResult{}, errors.New("physical handoff root is unavailable")
	}
	defer claim.Release()
	if err := execenv.VerifyPhysicalGeneration(input.Reservation, preparation.identity); err != nil {
		return environmentHandoffResult{}, err
	}
	reserved, err := execenv.PhysicalRootReserved(input.Reservation.RootDir)
	if err != nil {
		return environmentHandoffResult{}, err
	}
	if !reserved && !durable.HandoffRecovering {
		return environmentHandoffResult{}, errors.New("physical handoff reservation is unconfirmed")
	}
	// Journal the disposition before retiring the reservation. A failed final
	// persistence remains retryable under the same generation and kernel claim.
	preparation.HandoffRecovering = true
	if err := o.persist(preparation); err != nil {
		return environmentHandoffResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return environmentHandoffResult{}, err
	}
	if reserved {
		if err := execenv.ConfirmPhysicalRoot(input.Reservation, preparation.identity); err != nil {
			return environmentHandoffResult{}, err
		}
	}
	preparation.Aborted = true
	preparation.Outcome = &execenv.LocalWorktreeOutcome{}
	if err := o.persist(preparation); err != nil {
		preparation.Aborted = false
		preparation.Outcome = nil
		return environmentHandoffResult{}, err
	}
	return environmentHandoffResult{Settled: true}, nil
}
