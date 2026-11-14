package execenv

// WorktreeInspection carries the read-only delivery facts needed by a task worker.
// Physical finalization and its shared-directory lease remain with the environment owner.
type WorktreeInspection struct {
	GitRoot             string
	Path                string
	Branch              string
	BaseCommit          string
	ReplayConflicts     []string
	TracksState         bool
	CommitsAllLeftovers bool
}

func (w *LocalWorktree) Inspection() *WorktreeInspection {
	if w == nil {
		return nil
	}
	return &WorktreeInspection{GitRoot: w.GitRoot, Path: w.Path, Branch: w.Branch, BaseCommit: w.BaseCommit, ReplayConflicts: w.ReplayConflicts, TracksState: w.tracksState, CommitsAllLeftovers: w.CommitsAllLeftovers()}
}

func (i *WorktreeInspection) CheckDelivery() *DeliveryProblem {
	if i == nil {
		return nil
	}
	worktree := &LocalWorktree{GitRoot: i.GitRoot, Path: i.Path, Branch: i.Branch, BaseCommit: i.BaseCommit, tracksState: i.TracksState, snapshotPending: i.CommitsAllLeftovers}
	return worktree.CheckDelivery()
}
