package execenv

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PhysicalParticipant identifies the actual worker-held kernel lease. It carries
// no authority to select a different checkout or delivery namespace.
type PhysicalParticipant struct {
	Path    string                 `json:"path"`
	Name    string                 `json:"name"`
	Receipt SharedWorktreeDelivery `json:"receipt"`
}

// PhysicalFinishPermit authorizes only closure of the named participant FD.
type PhysicalFinishPermit struct {
	Participant PhysicalParticipant `json:"participant"`
	ID          string              `json:"id"`
}

// PhysicalFinish holds admission exclusion from receipt persistence through
// kernel release verification and physical settlement. Close preserves a durable
// unresolved fence, so losing the service cannot admit a new writer accidentally.
type PhysicalFinish struct {
	mu     sync.Mutex
	permit PhysicalFinishPermit
	dir    string
	unlock func()
}

var ErrPhysicalFinishPending = errors.New("physical finish awaits reconciliation")

// PhysicalParticipant returns the receipt bound to this worker's live FD.
func (l *SharedDirectoryLease) PhysicalParticipant() (PhysicalParticipant, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished || l.run.TaskID == "" {
		return PhysicalParticipant{}, errors.New("physical participant is not a live task")
	}
	return PhysicalParticipant{Path: l.path, Name: l.name, Receipt: l.run}, nil
}

func physicalFinishFile(dir, name string) string { return filepath.Join(dir, "finish-"+name+".json") }

func pendingPhysicalFinish(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "finish-participant-") {
			return true, nil
		}
	}
	return false, nil
}

// BeginPhysicalFinish is called by the physical owner before the worker closes
// its FD. A repeated call recovers the exact durable permit after service loss.
func BeginPhysicalFinish(ctx context.Context, participant PhysicalParticipant) (*PhysicalFinish, PhysicalFinishPermit, error) {
	if participant.Path == "" || !strings.HasPrefix(participant.Name, "participant-") || filepath.Base(participant.Name) != participant.Name || participant.Receipt.TaskID == "" {
		return nil, PhysicalFinishPermit{}, errors.New("invalid physical participant")
	}
	canonical, err := filepath.EvalSymlinks(participant.Path)
	if err != nil {
		return nil, PhysicalFinishPermit{}, err
	}
	if canonical != participant.Path {
		return nil, PhysicalFinishPermit{}, errors.New("physical participant path changed")
	}
	dir, err := sharedDirectoryStateDir(canonical)
	if err != nil {
		return nil, PhysicalFinishPermit{}, err
	}
	unlock, err := lockSharedDirectoryState(ctx, dir)
	if err != nil {
		return nil, PhysicalFinishPermit{}, err
	}
	success := false
	defer func() {
		if !success {
			unlock()
		}
	}()
	permit := PhysicalFinishPermit{Participant: participant, ID: rand.Text()}
	data, err := os.ReadFile(physicalFinishFile(dir, participant.Name))
	if err == nil {
		if err = json.Unmarshal(data, &permit); err != nil {
			return nil, PhysicalFinishPermit{}, err
		}
		if permit.Participant != participant || permit.ID == "" {
			return nil, PhysicalFinishPermit{}, errors.New("physical finish identity changed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, PhysicalFinishPermit{}, err
	} else {
		if pending, err := pendingPhysicalFinish(dir); err != nil {
			return nil, PhysicalFinishPermit{}, err
		} else if pending {
			return nil, PhysicalFinishPermit{}, ErrPhysicalFinishPending
		}
		data, err = os.ReadFile(filepath.Join(dir, participant.Name))
		if err != nil {
			return nil, PhysicalFinishPermit{}, err
		}
		var receipt SharedWorktreeDelivery
		if err = json.Unmarshal(data, &receipt); err != nil {
			return nil, PhysicalFinishPermit{}, err
		}
		if receipt != participant.Receipt {
			return nil, PhysicalFinishPermit{}, errors.New("physical participant receipt changed")
		}
		if err = preserveSharedWorktreeReceipt(dir, receipt); err != nil {
			return nil, PhysicalFinishPermit{}, err
		}
		data, err = json.Marshal(permit)
		if err != nil {
			return nil, PhysicalFinishPermit{}, err
		}
		if err = writeFileAtomic(physicalFinishFile(dir, participant.Name), data, 0600); err != nil {
			return nil, PhysicalFinishPermit{}, err
		}
	}
	success = true
	return &PhysicalFinish{permit: permit, dir: dir, unlock: unlock}, permit, nil
}

// ReleasePhysicalParticipant closes only the caller-owned FD after a matching
// permit. It never enters the state guard held by the environment service.
func (l *SharedDirectoryLease) ReleasePhysicalParticipant(permit PhysicalFinishPermit) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if permit.ID == "" || permit.Participant.Path != l.path || permit.Participant.Name != l.name || permit.Participant.Receipt != l.run {
		return errors.New("physical finish permit mismatch")
	}
	data, err := os.ReadFile(physicalFinishFile(l.stateDir, l.name))
	if err != nil {
		return err
	}
	var durable PhysicalFinishPermit
	if err = json.Unmarshal(data, &durable); err != nil {
		return err
	}
	if durable != permit {
		return errors.New("physical finish permit superseded")
	}
	if !l.finished {
		releaseLockFile(l.file)
		l.finished = true
	}
	return nil
}

// Confirm verifies kernel release before settling under the same admission
// guard. Failure leaves the durable receipt and fence available for retry.
func (f *PhysicalFinish) Confirm(permit PhysicalFinishPermit, settle func(last bool) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unlock == nil || permit != f.permit {
		return errors.New("physical finish is not current")
	}
	participantPath := filepath.Join(f.dir, permit.Participant.Name)
	info, err := os.Lstat(participantPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("physical participant is not a regular file")
	}
	file, err := openLockFile(participantPath)
	if err != nil {
		return err
	}
	locked, err := lockFileExclusiveNonBlocking(file)
	if err != nil {
		file.Close()
		return err
	}
	if !locked {
		file.Close()
		return errors.New("physical participant still holds its kernel lease")
	}
	defer releaseLockFile(file)
	lockedInfo, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, lockedInfo) {
		return errors.New("physical participant changed before release proof")
	}
	data, err := os.ReadFile(participantPath)
	if err != nil {
		return err
	}
	var receipt SharedWorktreeDelivery
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	if receipt != permit.Participant.Receipt {
		return errors.New("physical participant receipt changed before settlement")
	}
	live, err := liveSharedDirectoryUsers(f.dir, permit.Participant.Name)
	if err != nil {
		return err
	}
	if live == 0 {
		if err = restoreSharedDirectoryGuard(f.dir); err != nil {
			return err
		}
	}
	if settle != nil {
		if err = settle(live == 0); err != nil {
			return err
		}
	}
	if err = os.Remove(filepath.Join(f.dir, permit.Participant.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.Remove(physicalFinishFile(f.dir, permit.Participant.Name)); err != nil {
		return err
	}
	f.unlock()
	f.unlock = nil
	return nil
}

// Close releases service exclusion while retaining the unresolved finish fence.
func (f *PhysicalFinish) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unlock != nil {
		f.unlock()
		f.unlock = nil
	}
}

// FinalizePhysical settles the service-owned worktree only after the separate
// worker has released its actual participant, keeping last-borrower settlement
// under the same guard as the original in-process Finish path.
func (w *LocalWorktree) FinalizePhysical(finish *PhysicalFinish, permit PhysicalFinishPermit, logger *slog.Logger, record func(LocalWorktreeOutcome, error) error) (LocalWorktreeOutcome, error) {
	if w == nil || finish == nil || permit.Participant.Path != w.Path || permit.Participant.Receipt.WorkDir != w.WorkDir || permit.Participant.Receipt.Branch != w.Branch {
		return LocalWorktreeOutcome{}, errors.New("physical finish worktree identity mismatch")
	}
	var outcome LocalWorktreeOutcome
	var settlementErr error
	err := finish.Confirm(permit, func(last bool) error {
		if !last {
			outcome = LocalWorktreeOutcome{DeliveryPending: true, PreservedPath: w.Path}
			if record != nil {
				return record(outcome, nil)
			}
			return nil
		}
		outcome, settlementErr = w.finalizeOwned(logger)
		if settlementErr == nil {
			if err := resolveSharedWorktreeReceipts(finish.dir, outcome.Commit); err != nil {
				return err
			}
		}
		if record != nil {
			return record(outcome, settlementErr)
		}
		return settlementErr
	})
	return outcome, errors.Join(err, settlementErr)
}

// PhysicalParticipant returns the task owner's actual worktree participant.
func (w *LocalWorktree) PhysicalParticipant() (PhysicalParticipant, error) {
	if w == nil || w.executionLease == nil {
		return PhysicalParticipant{}, errors.New("worktree has no worker participant")
	}
	return w.executionLease.PhysicalParticipant()
}

// ReleasePhysicalParticipant applies a service permit to this worker's own FD.
func (w *LocalWorktree) ReleasePhysicalParticipant(permit PhysicalFinishPermit) error {
	if w == nil || w.executionLease == nil {
		return errors.New("worktree has no worker participant")
	}
	return w.executionLease.ReleasePhysicalParticipant(permit)
}

// HasOtherUsers samples live kernel participants while this lease pins the code.
func (l *SharedDirectoryLease) HasOtherUsers(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return false, errors.New("shared participant has finished")
	}
	unlock, err := lockSharedDirectoryState(ctx, l.stateDir)
	if err != nil {
		return false, err
	}
	defer unlock()
	count, err := liveSharedDirectoryUsers(l.stateDir, l.name)
	return count > 0, err
}

// UseSharedDirectoryForTask binds a generic task participant without creating a
// Git delivery receipt unless the caller explicitly supplies its report namespace.
func UseSharedDirectoryForTask(ctx context.Context, path string, receipt SharedWorktreeDelivery) (*SharedDirectoryLease, error) {
	lease, err := UseSharedDirectory(ctx, path)
	if err != nil {
		return nil, err
	}
	if err = lease.bindWorktreeRun(receipt); err != nil {
		lease.Finish(ctx, nil)
		return nil, err
	}
	return lease, nil
}

// ReleaseUnsettled mirrors process exit: the durable participant record remains
// available to recover its delivery, but an ended task cannot retain a live FD.
func (l *SharedDirectoryLease) ReleaseUnsettled() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.finished {
		releaseLockFile(l.file)
		l.finished = true
	}
}

// VerifyPhysicalParticipant checks a live, exact worker FD while holding its
// metadata guard. A descriptor string or a stale participant file is insufficient.
func VerifyPhysicalParticipant(ctx context.Context, participant PhysicalParticipant) error {
	if participant.Path == "" || filepath.Base(participant.Name) != participant.Name || !strings.HasPrefix(participant.Name, "participant-") {
		return errors.New("invalid physical participant")
	}
	dir, err := sharedDirectoryStateDir(participant.Path)
	if err != nil {
		return err
	}
	unlock, err := lockSharedDirectoryState(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	file, err := os.OpenFile(filepath.Join(dir, participant.Name), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	locked, err := lockFileExclusiveNonBlocking(file)
	if err != nil {
		file.Close()
		return err
	}
	if locked {
		releaseLockFile(file)
		return errors.New("physical participant is not held by a worker")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 128<<10))
	if err != nil {
		return err
	}
	var receipt SharedWorktreeDelivery
	if err = json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	if receipt != participant.Receipt {
		return errors.New("physical worker receipt changed")
	}
	return nil
}
