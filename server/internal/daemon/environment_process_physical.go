package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// Physical results carry facts needed by private preparation, not the service's
// mutable Git settlement state or any provider configuration.
type environmentPhysicalResult struct {
	Reused            bool                            `json:"reused"`
	RetainCheckout    bool                            `json:"retain_checkout"`
	Reservation       execenv.PhysicalRootReservation `json:"reservation"`
	CodeRoot          string                          `json:"code_root"`
	WorkDir           string                          `json:"work_dir"`
	LocalDirectory    bool                            `json:"local_directory"`
	PrivateCheckout   bool                            `json:"private_checkout"`
	Branch            string                          `json:"branch,omitempty"`
	BaseCommit        string                          `json:"base_commit,omitempty"`
	WorktreePath      string                          `json:"worktree_path,omitempty"`
	GitRoot           string                          `json:"git_root,omitempty"`
	Continued         bool                            `json:"continued,omitempty"`
	DirtyBaseCaptured bool                            `json:"dirty_base_captured,omitempty"`
	ReplayConflicts   []string                        `json:"replay_conflicts,omitempty"`
}

type environmentPhysicalPreparation struct {
	checkoutIdentity os.FileInfo
	Attached         bool `json:"attached"`
	codeIdentity     os.FileInfo
	Selected         bool                     `json:"selected"`
	Consumer         execenv.WorktreeConsumer `json:"consumer"`
	bridge           *execenv.SharedDirectoryLease
	Result           environmentPhysicalResult     `json:"result"`
	Physical         *execenv.Environment          `json:"physical"`
	Participant      *execenv.PhysicalParticipant  `json:"participant,omitempty"`
	Outcome          *execenv.LocalWorktreeOutcome `json:"outcome,omitempty"`
	Failure          string                        `json:"failure,omitempty"`
	RootConfirmed    bool                          `json:"root_confirmed"`
	identity         os.FileInfo
	finish           *execenv.PhysicalFinish
}

type environmentPhysicalOwner struct {
	releaseConsumer func(*environmentPhysicalPreparation) error
	// onSettle is invoked exactly once when a preparation's finish outcome is
	// durably recorded (the worker's Git user is no longer active). The service
	// uses it to release the maintenance barrier held while the worker ran.
	onSettle     func()
	mu           sync.Mutex
	root         string
	journal      string
	logger       *slog.Logger
	preparations map[string]*environmentPhysicalPreparation
}

func newEnvironmentPhysicalOwner(root, journal string, logger *slog.Logger) (*environmentPhysicalOwner, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(journal) {
		return nil, errors.New("physical owner requires absolute configured roots")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	root = canonical
	if err := os.MkdirAll(journal, 0700); err != nil {
		return nil, err
	}
	owner := &environmentPhysicalOwner{root: root, journal: journal, logger: logger, preparations: map[string]*environmentPhysicalPreparation{}}
	if err := owner.recoverJournal(); err != nil {
		return nil, err
	}
	return owner, nil
}

// recoverJournal loads durable preparations written by a prior owner so a
// restarted environment service keeps them as protected, fail-closed records
// instead of forgetting that a root is in flight. The service-owned kernel
// identities (FileInfo, bridge, finish) cannot be reconstructed, so a recovered
// preparation is re-verified before it acts and never fabricates authority or
// resets/deletes the root: an in-flight one fail-closes until the control
// reconciles it, while a completed one stays idempotent from its recorded
// Outcome.
func (o *environmentPhysicalOwner) recoverJournal() error {
	entries, err := os.ReadDir(o.journal)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.journal, name))
		if err != nil {
			if o.logger != nil {
				o.logger.Error("physical journal recovery: cannot read record", "file", name, "error", err)
			}
			continue
		}
		preparation := &environmentPhysicalPreparation{}
		if err := json.Unmarshal(data, preparation); err != nil {
			// A malformed record is not permission to forget protection: the
			// execenv reservation still guards the root, so skip it here.
			if o.logger != nil {
				o.logger.Error("physical journal recovery: malformed record", "file", name, "error", err)
			}
			continue
		}
		if preparation.Result.Reservation.ID == "" {
			continue
		}
		o.preparations[preparation.Result.Reservation.ID] = preparation
	}
	return nil
}

func (o *environmentPhysicalOwner) persist(preparation *environmentPhysicalPreparation) error {
	data, err := json.Marshal(preparation)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(o.journal, ".physical-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(o.journal, preparation.Result.Reservation.ID+".json"))
}

func (o *environmentPhysicalOwner) prepare(ctx context.Context, input execenv.PhysicalPrepareParams, metadata ...*execenv.GCMeta) (environmentPhysicalResult, error) {
	if err := ctx.Err(); err != nil {
		return environmentPhysicalResult{}, err
	}
	// The root is process configuration, never supplied by an operation payload.
	input.WorkspacesRoot = o.root
	env, err := execenv.PreparePhysical(input, o.logger)
	if err != nil {
		return environmentPhysicalResult{}, err
	}
	defer env.ReleaseLock()
	if len(metadata) > 0 && metadata[0] != nil {
		meta := metadata[0]
		if meta.TaskID != input.TaskID || meta.WorkspaceID != input.WorkspaceID || meta.RuntimeID != input.RuntimeID {
			return environmentPhysicalResult{}, errors.New("physical task metadata scope mismatch")
		}
		if err := execenv.SaveGCMeta(env.RootDir, *meta); err != nil {
			return environmentPhysicalResult{}, err
		}
	}
	reservation, identity, err := env.ReservePhysicalRoot(input.WorkspaceID, input.TaskID)
	if err != nil {
		return environmentPhysicalResult{}, err
	}
	result := physicalResultForEnvironment(env, reservation)
	preparation := &environmentPhysicalPreparation{Result: result, Physical: env, identity: identity}
	checkout := env.WorkDir
	if env.LocalWorktree != nil {
		checkout = env.LocalWorktree.Path
	}
	preparation.checkoutIdentity, err = os.Stat(checkout)
	if err != nil {
		return environmentPhysicalResult{}, err
	}
	if err = o.persist(preparation); err != nil {
		return environmentPhysicalResult{}, err
	}
	o.mu.Lock()
	o.preparations[reservation.ID] = preparation
	o.mu.Unlock()
	return result, nil
}

func (o *environmentPhysicalOwner) confirmRoot(reservation execenv.PhysicalRootReservation) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	preparation := o.preparations[reservation.ID]
	if preparation == nil || preparation.Result.Reservation != reservation {
		return errors.New("physical preparation is unknown")
	}
	if preparation.RootConfirmed {
		return nil
	}
	if err := execenv.ConfirmPhysicalRoot(reservation, preparation.identity); err != nil {
		return err
	}
	preparation.RootConfirmed = true
	return o.persist(preparation)
}

func (o *environmentPhysicalOwner) beginFinish(ctx context.Context, id string, participant execenv.PhysicalParticipant, abortReason ...string) (execenv.PhysicalFinishPermit, error) {
	o.mu.Lock()
	preparation := o.preparations[id]
	if preparation == nil || !preparation.RootConfirmed || preparation.Outcome != nil {
		o.mu.Unlock()
		return execenv.PhysicalFinishPermit{}, errors.New("physical worktree is not confirmed")
	}
	expectedPath, expectedBranch := preparation.Physical.WorkDir, ""
	if wt := preparation.Physical.LocalWorktree; wt != nil {
		expectedPath, expectedBranch = wt.Path, wt.Branch
	}
	if participant.Receipt.TaskID != preparation.Result.Reservation.TaskID || participant.Path != expectedPath || participant.Receipt.WorkDir != preparation.Physical.WorkDir || participant.Receipt.Branch != expectedBranch || preparation.Physical.LocalWorktree == nil && participant.Receipt.Namespace != "" {
		o.mu.Unlock()
		return execenv.PhysicalFinishPermit{}, errors.New("physical participant scope mismatch")
	}
	if preparation.finish != nil {
		o.mu.Unlock()
		return execenv.PhysicalFinishPermit{}, errors.New("physical finish is already in progress")
	}
	o.mu.Unlock()
	if err := verifyPhysicalPreparationIdentity(preparation); err != nil {
		return execenv.PhysicalFinishPermit{}, err
	}
	finish, permit, err := execenv.BeginPhysicalFinish(ctx, participant)
	if err != nil {
		return permit, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(abortReason) > 0 && abortReason[0] != "" && preparation.Physical.LocalWorktree != nil {
		preparation.Physical.LocalWorktree.AbortWithReason(errors.New("worker private cleanup incomplete"))
	}
	preparation.Participant = &participant
	if err = o.persist(preparation); err != nil {
		finish.Close()
		return execenv.PhysicalFinishPermit{}, err
	}
	preparation.finish = finish
	return permit, nil
}

func (o *environmentPhysicalOwner) confirmFinish(id string, permit execenv.PhysicalFinishPermit) (execenv.LocalWorktreeOutcome, error) {
	o.mu.Lock()
	preparation := o.preparations[id]
	if preparation == nil || preparation.Participant == nil || *preparation.Participant != permit.Participant {
		o.mu.Unlock()
		return execenv.LocalWorktreeOutcome{}, errors.New("physical finish scope mismatch")
	}
	if preparation.Outcome != nil {
		outcome := *preparation.Outcome
		failure := preparation.Failure
		o.mu.Unlock()
		if failure != "" {
			return outcome, errors.New(failure)
		}
		return outcome, nil
	}
	finish := preparation.finish
	o.mu.Unlock()
	if err := verifyPhysicalPreparationIdentity(preparation); err != nil {
		return execenv.LocalWorktreeOutcome{PreservedPath: preparation.Physical.WorkDir}, err
	}
	if finish == nil {
		return execenv.LocalWorktreeOutcome{}, errors.New("physical finish requires reconciliation")
	}
	if preparation.Selected {
		current, err := os.Stat(preparation.Result.CodeRoot)
		if err != nil || preparation.codeIdentity == nil || !os.SameFile(current, preparation.codeIdentity) {
			return execenv.LocalWorktreeOutcome{PreservedPath: preparation.Physical.WorkDir}, errors.New("physical code root changed before settlement")
		}
	}
	record := func(outcome execenv.LocalWorktreeOutcome, settlementErr error) error {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.releaseConsumer != nil && preparation.Selected {
			if err := o.releaseConsumer(preparation); err != nil {
				return err
			}
		}
		preparation.Outcome = &outcome
		if settlementErr != nil {
			preparation.Failure = settlementErr.Error()
		}
		if err := o.persist(preparation); err != nil {
			preparation.Outcome = nil
			preparation.Failure = ""
			return err
		}
		// The finish is durable: the worker's Git user is done, so release the
		// maintenance barrier. Called once per preparation (record runs only
		// while Outcome is unset).
		if o.onSettle != nil {
			o.onSettle()
		}
		return nil
	}
	var outcome execenv.LocalWorktreeOutcome
	var err error
	if preparation.Physical.LocalWorktree != nil {
		outcome, err = preparation.Physical.LocalWorktree.FinalizePhysical(finish, permit, o.logger, record)
	} else {
		err = finish.Confirm(permit, func(bool) error { return record(outcome, nil) })
	}
	if err != nil {
		return outcome, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	preparation.finish = nil
	return outcome, nil
}

// verifyCompletionGeneration fences a late or old completion against the
// physical root's current generation. The owner keeps the original FileInfo, so
// it detects both a reset root and a later re-claim of the same path. A missing
// preparation is unknown, never permission to write metadata for a later owner.
func (o *environmentPhysicalOwner) verifyCompletionGeneration(id string) error {
	o.mu.Lock()
	preparation := o.preparations[id]
	o.mu.Unlock()
	if preparation == nil {
		return errors.New("physical completion preparation is unknown")
	}
	return execenv.VerifyPhysicalGeneration(preparation.Result.Reservation, preparation.identity)
}

func (o *environmentPhysicalOwner) close() {
	o.mu.Lock()
	var finishes []*execenv.PhysicalFinish
	var bridges []*execenv.SharedDirectoryLease
	for _, preparation := range o.preparations {
		if preparation.bridge != nil {
			bridges = append(bridges, preparation.bridge)
			preparation.bridge = nil
		}
		if preparation.finish != nil {
			finishes = append(finishes, preparation.finish)
			preparation.finish = nil
		}
	}
	o.mu.Unlock()
	for _, finish := range finishes {
		finish.Close()
	}
	for _, bridge := range bridges {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		bridge.Finish(ctx, nil)
		cancel()
	}
}

func verifyPhysicalPreparationIdentity(preparation *environmentPhysicalPreparation) error {
	if err := execenv.VerifyPhysicalGeneration(preparation.Result.Reservation, preparation.identity); err != nil {
		return err
	}
	path := preparation.Physical.WorkDir
	if preparation.Physical.LocalWorktree != nil {
		path = preparation.Physical.LocalWorktree.Path
	}
	info, err := os.Stat(path)
	if err != nil || preparation.checkoutIdentity == nil || !os.SameFile(preparation.checkoutIdentity, info) {
		return errors.New("physical checkout identity changed")
	}
	return nil
}
