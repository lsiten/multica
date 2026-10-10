package execenv

import (
	"context"
	"errors"
	"time"
)

// PhysicalWorktreeControl delegates Git settlement while the task owner keeps
// the real participant FD. Implementations carry only an exact physical handle.
type PhysicalWorktreeControl interface {
	Attach(context.Context, PhysicalParticipant) error
	BeginFinish(context.Context, PhysicalParticipant, string) (PhysicalFinishPermit, error)
	ConfirmFinish(context.Context, PhysicalFinishPermit) (LocalWorktreeOutcome, error)
}

// SetPhysicalControl binds a verified physical result to its service controller.
// The service's mutable branch/snapshot state is never reconstructed by workers.
func (w *LocalWorktree) SetPhysicalControl(control PhysicalWorktreeControl) {
	w.physicalControl = control
}

func (w *LocalWorktree) finalizeThroughPhysicalOwner() (LocalWorktreeOutcome, error) {
	preserved := LocalWorktreeOutcome{PreservedPath: w.Path}
	if w.executionLease == nil {
		return preserved, errors.New("physical worktree has no worker lease")
	}
	defer w.executionLease.ReleaseUnsettled()
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	participant, err := w.executionLease.PhysicalParticipant()
	if err != nil {
		return preserved, err
	}
	reason := ""
	if w.aborted != nil {
		reason = "worker private-file cleanup was not confirmed"
	}
	permit, err := w.physicalControl.BeginFinish(ctx, participant, reason)
	if err != nil {
		return preserved, err
	}
	if err = w.executionLease.ReleasePhysicalParticipant(permit); err != nil {
		return preserved, err
	}
	return w.physicalControl.ConfirmFinish(ctx, permit)
}

// FinishPhysicalUse is the non-Git equivalent of worktree finalization.
func FinishPhysicalUse(lease *SharedDirectoryLease, control PhysicalWorktreeControl) error {
	if lease == nil {
		return nil
	}
	defer lease.ReleaseUnsettled()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	participant, err := lease.PhysicalParticipant()
	if err != nil {
		return err
	}
	permit, err := control.BeginFinish(ctx, participant, "")
	if err != nil {
		return err
	}
	if err = lease.ReleasePhysicalParticipant(permit); err != nil {
		return err
	}
	_, err = control.ConfirmFinish(ctx, permit)
	return err
}
