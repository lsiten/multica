package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	terminalReportRecordVersion = 1
	terminalReportReplayWorkers = 4

	terminalReportReplayInitialBackoff = 5 * time.Second
	terminalReportReplayMaxBackoff     = 5 * time.Minute

	// Only an unchanged request that receives three explicit permanent
	// rejections over at least ten minutes is quarantined. The count rejects a
	// one-off proxy response; the age prevents reconnect/startup nudges from
	// turning three rapid attempts into a false permanent verdict.
	terminalReportPermanentRejectionLimit = 3
	terminalReportPermanentRejectionAge   = 10 * time.Minute
)

// persistedTerminalTaskReport is the versioned on-disk form of one terminal
// callback. It deliberately contains no auth token: replay always uses the
// daemon's current credential, while the potentially sensitive agent output is
// protected by owner-only modes on Unix. On Windows Go's mode bits are not an
// ACL boundary, so protection comes from the current user's profile/workspace
// ACL; the queue never carries an auth token on either platform.
//
// Version stays at 1 because the rejection fields are additive and older
// daemons ignore unknown JSON fields. A future incompatible version is left
// untouched and reported on every replay pass; downgrading must never delete a
// payload merely because the older binary cannot decode it.
type persistedTerminalTaskReport struct {
	Version                 int                              `json:"version"`
	CreatedAt               time.Time                        `json:"created_at"`
	Kind                    string                           `json:"kind"`
	TaskID                  string                           `json:"task_id"`
	Output                  string                           `json:"output,omitempty"`
	BranchName              string                           `json:"branch_name,omitempty"`
	ErrorMessage            string                           `json:"error,omitempty"`
	SessionID               string                           `json:"session_id,omitempty"`
	WorkDir                 string                           `json:"work_dir,omitempty"`
	DurableWorkDir          string                           `json:"durable_work_dir,omitempty"`
	FailureReason           string                           `json:"failure_reason,omitempty"`
	SessionRolloutMissing   bool                             `json:"session_rollout_missing,omitempty"`
	RetiredSessionID        string                           `json:"retired_session_id,omitempty"`
	JevVerification         *JevVerification                 `json:"jev_verification,omitempty"`
	WorktreeDeliveryPending bool                             `json:"worktree_delivery_pending,omitempty"`
	WorktreeCommit          string                           `json:"worktree_commit,omitempty"`
	AccountID               string                           `json:"account_id,omitempty"`
	ExecutionID             string                           `json:"execution_id,omitempty"`
	Messages                []TaskMessageData                `json:"messages,omitempty"`
	Usage                   []TaskUsageEntry                 `json:"usage,omitempty"`
	Execution               *protocol.ExecutionIdentity      `json:"execution,omitempty"`
	ApplicationObservation  *protocol.ApplicationObservation `json:"application_observation,omitempty"`
	RuntimeID               string                           `json:"runtime_id,omitempty"`
	ApplicationDesiredState string                           `json:"application_desired_state,omitempty"`
	ApplicationIntentID     string                           `json:"application_intent_id,omitempty"`
	ApplicationStep         *applicationStepReceipt          `json:"application_step,omitempty"`

	PermanentRejectionCount   int        `json:"permanent_rejection_count,omitempty"`
	FirstPermanentRejectionAt *time.Time `json:"first_permanent_rejection_at,omitempty"`
	LastPermanentRejectionAt  *time.Time `json:"last_permanent_rejection_at,omitempty"`
	LastPermanentStatus       int        `json:"last_permanent_status,omitempty"`
	QuarantinedAt             *time.Time `json:"quarantined_at,omitempty"`
}

type pendingTerminalTaskReport struct {
	fileName string
	report   terminalTaskReport
}

type terminalReportStoreStats struct {
	PendingCount int
	PendingBytes int64
	FailedCount  int
	FailedBytes  int64
}

type terminalReportNamespaceStats struct {
	name  string
	stats terminalReportStoreStats
}

// terminalReportStore is a file-backed outbox. One pending file per task keeps each
// acknowledgement independent, and hashing task IDs prevents a malformed or
// tampered task ID from becoming a path traversal primitive.
type terminalReportStore struct {
	root      string
	namespace string
	dir       string
	mu        sync.Mutex

	// accountAware, when set by bindAccount, puts the store in the F2
	// account-scoped mode: only records whose account matches the current
	// account are adopted for upload. A record with a different or empty
	// account (a legacy v1 record, or another account's record) is preserved
	// but never adopted, so a login change cannot silently replay the other
	// account's pending terminal report.
	accountAware bool
	account      string
}

func newTerminalReportStore(cfg Config) *terminalReportStore {
	if strings.TrimSpace(cfg.WorkspacesRoot) == "" {
		return nil
	}
	identity := strings.TrimRight(cfg.ServerBaseURL, "/") + "\x00" + cfg.Profile + "\x00" + cfg.DaemonID
	sum := sha256.Sum256([]byte(identity))
	namespace := hex.EncodeToString(sum[:16])
	root := filepath.Join(cfg.WorkspacesRoot, ".pending-terminal-reports", "v1")
	return &terminalReportStore{
		root:      root,
		namespace: namespace,
		dir:       filepath.Join(root, namespace),
	}
}

func (s *terminalReportStore) failedDir() string { return filepath.Join(s.dir, "failed") }

// bindAccount moves the store into account-aware mode. It is called once
// after the daemon has resolved its authenticated account, before the
// replay loop starts. An empty account leaves the store in its legacy
// behaviour so pre-account daemons and the existing single-process tests
// keep uploading every record as before.
func (s *terminalReportStore) bindAccount(account string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(account) == "" {
		return
	}
	s.account = account
	s.accountAware = true
}

func terminalReportFileName(taskID string) string {
	sum := sha256.Sum256([]byte(taskID))
	return hex.EncodeToString(sum[:]) + ".json"
}

func terminalReportsEqual(left, right terminalTaskReport) bool {
	// Decoding changes pointer identities and omitted empty maps. Compare the
	// persisted payload without timestamps or rejection bookkeeping.
	leftPayload, leftErr := terminalReportPayload(left)
	rightPayload, rightErr := terminalReportPayload(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftPayload, rightPayload)
}

func terminalReportPayload(report terminalTaskReport) ([]byte, error) {
	record, err := persistedTerminalReport(report, time.Time{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func terminalReportKindName(kind terminalTaskReportKind) (string, error) {
	switch kind {
	case terminalTaskReportComplete:
		return "complete", nil
	case terminalTaskReportFail:
		return "fail", nil
	case taskMessagesReport:
		return "messages", nil
	case taskUsageReport:
		return "usage", nil
	case applicationObservationReport:
		return "application-observation", nil
	case applicationStepReport:
		return "application-step", nil
	default:
		return "", fmt.Errorf("unsupported terminal task report kind %d", kind)
	}
}

func persistedTerminalReport(report terminalTaskReport, createdAt time.Time) (persistedTerminalTaskReport, error) {
	if strings.TrimSpace(report.taskID) == "" {
		return persistedTerminalTaskReport{}, errors.New("terminal task report has no task id")
	}
	if report.kind == applicationObservationReport && (report.applicationObservation == nil || report.runtimeID == "" || report.applicationObservation.InstanceID != report.taskID) {
		return persistedTerminalTaskReport{}, errors.New("invalid application observation report")
	}
	if report.kind == applicationStepReport && (report.applicationStep == nil || report.applicationStep.validate(report.runtimeID, report.taskID) != nil) {
		return persistedTerminalTaskReport{}, errors.New("invalid application step report")
	}
	kind, err := terminalReportKindName(report.kind)
	if err != nil {
		return persistedTerminalTaskReport{}, err
	}
	return persistedTerminalTaskReport{
		Version:                 terminalReportRecordVersion,
		CreatedAt:               createdAt.UTC(),
		Kind:                    kind,
		TaskID:                  report.taskID,
		Output:                  report.output,
		BranchName:              report.branchName,
		ErrorMessage:            report.errorMessage,
		SessionID:               report.sessionID,
		WorkDir:                 report.workDir,
		DurableWorkDir:          report.durableWorkDir,
		FailureReason:           report.failureReason,
		SessionRolloutMissing:   report.sessionRolloutMissing,
		RetiredSessionID:        report.retiredSessionID,
		JevVerification:         report.jevVerification,
		WorktreeDeliveryPending: report.worktreeDeliveryPending,
		WorktreeCommit:          report.worktreeCommit,
		AccountID:               report.accountID,
		ExecutionID:             report.executionID,
		Messages:                report.messages,
		Usage:                   report.usage,
		Execution:               report.execution,
		ApplicationObservation:  report.applicationObservation,
		RuntimeID:               report.runtimeID,
		ApplicationDesiredState: report.applicationDesiredState,
		ApplicationIntentID:     report.applicationIntentID,
		ApplicationStep:         report.applicationStep,
	}, nil
}

func (record persistedTerminalTaskReport) terminalReport() (terminalTaskReport, error) {
	if record.Version != terminalReportRecordVersion {
		return terminalTaskReport{}, fmt.Errorf("unsupported terminal report version %d", record.Version)
	}
	if strings.TrimSpace(record.TaskID) == "" {
		return terminalTaskReport{}, errors.New("terminal report has no task id")
	}
	var kind terminalTaskReportKind
	switch record.Kind {
	case "complete":
		kind = terminalTaskReportComplete
	case "fail":
		kind = terminalTaskReportFail
	case "messages":
		kind = taskMessagesReport
	case "usage":
		kind = taskUsageReport
	case "application-observation":
		kind = applicationObservationReport
		if record.ApplicationObservation == nil || record.RuntimeID == "" || record.ApplicationObservation.InstanceID != record.TaskID {
			return terminalTaskReport{}, errors.New("invalid queued application observation")
		}
	case "application-step":
		kind = applicationStepReport
		if record.ApplicationStep == nil || record.ApplicationStep.validate(record.RuntimeID, record.TaskID) != nil {
			return terminalTaskReport{}, errors.New("invalid queued application step")
		}
	default:
		return terminalTaskReport{}, fmt.Errorf("unsupported terminal report kind %q", record.Kind)
	}
	return terminalTaskReport{
		kind:                    kind,
		taskID:                  record.TaskID,
		output:                  record.Output,
		branchName:              record.BranchName,
		errorMessage:            record.ErrorMessage,
		sessionID:               record.SessionID,
		workDir:                 record.WorkDir,
		durableWorkDir:          record.DurableWorkDir,
		failureReason:           record.FailureReason,
		sessionRolloutMissing:   record.SessionRolloutMissing,
		retiredSessionID:        record.RetiredSessionID,
		jevVerification:         record.JevVerification,
		worktreeDeliveryPending: record.WorktreeDeliveryPending,
		worktreeCommit:          record.WorktreeCommit,
		accountID:               record.AccountID,
		executionID:             record.ExecutionID,
		messages:                record.Messages,
		usage:                   record.Usage,
		execution:               record.Execution,
		applicationObservation:  record.ApplicationObservation,
		runtimeID:               record.RuntimeID,
		applicationDesiredState: record.ApplicationDesiredState,
		applicationIntentID:     record.ApplicationIntentID,
		applicationStep:         record.ApplicationStep,
	}, nil
}

func (s *terminalReportStore) ensureDir() error {
	if s == nil {
		return errors.New("terminal report store is not configured")
	}
	return ensureTerminalReportDir(s.dir)
}

func ensureTerminalReportDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create terminal report queue: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect terminal report queue: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("terminal report queue is not a real directory: %s", dir)
	}
	// Tighten an existing directory as well as a newly-created one. Terminal
	// payloads may include private prompts, results, paths, and error details.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure terminal report queue: %w", err)
	}
	return nil
}

func (s *terminalReportStore) enqueue(report terminalTaskReport) error {
	record, err := persistedTerminalReport(report, time.Now())
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDir(); err != nil {
		return err
	}
	name := terminalReportFileName(report.storageKey())
	path := filepath.Join(s.dir, name)
	if existingBody, readErr := os.ReadFile(path); readErr == nil {
		existing, decodeErr := decodePersistedTerminalReport(existingBody)
		if decodeErr != nil {
			return fmt.Errorf("existing terminal report %s is unreadable: %w", name, decodeErr)
		}
		existingReport, decodeErr := existing.terminalReport()
		if decodeErr != nil {
			return fmt.Errorf("existing terminal report %s is invalid: %w", name, decodeErr)
		}
		if !terminalReportsEqual(existingReport, report) {
			if report.kind == applicationObservationReport && existingReport.kind == report.kind && existingReport.accountID == report.accountID && existingReport.applicationObservation != nil && report.applicationObservation != nil {
				if report.applicationObservation.Generation < existingReport.applicationObservation.Generation {
					return nil
				}
				return writeTerminalReportRecord(s.dir, name, record)
			}
			return fmt.Errorf("terminal report for task %s conflicts with the original pending payload", report.taskID)
		}
		return nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read existing terminal report %s: %w", name, readErr)
	}

	return writeTerminalReportRecord(s.dir, name, record)
}

// writeTerminalReportRecord publishes a fully-flushed record with an atomic
// rename. Callers hold the store mutex. Temp files use the task-derived prefix
// so startup recovery can validate where an interrupted write belonged.
func writeTerminalReportRecord(dir, name string, record persistedTerminalTaskReport) error {
	body, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode terminal report: %w", err)
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(dir, "."+strings.TrimSuffix(name, ".json")+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create terminal report temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("secure terminal report temp file: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		cleanup()
		return fmt.Errorf("write terminal report temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync terminal report temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close terminal report temp file: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish terminal report: %w", err)
	}
	if err := syncTerminalReportDir(dir); err != nil {
		return fmt.Errorf("sync terminal report queue after enqueue: %w", err)
	}
	return nil
}

func decodePersistedTerminalReport(body []byte) (persistedTerminalTaskReport, error) {
	var record persistedTerminalTaskReport
	if err := json.Unmarshal(body, &record); err != nil {
		return persistedTerminalTaskReport{}, err
	}
	return record, nil
}

func (s *terminalReportStore) list() ([]pendingTerminalTaskReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDir(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read terminal report queue: %w", err)
	}
	recoveryErr := s.recoverTempFiles(entries)
	entries, err = os.ReadDir(s.dir)
	if err != nil {
		return nil, errors.Join(recoveryErr, fmt.Errorf("reread terminal report queue: %w", err))
	}
	items := make([]pendingTerminalTaskReport, 0, len(entries))
	var errs []error
	if recoveryErr != nil {
		errs = append(errs, recoveryErr)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if readErr != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", entry.Name(), readErr))
			continue
		}
		record, decodeErr := decodePersistedTerminalReport(body)
		if decodeErr != nil {
			errs = append(errs, fmt.Errorf("decode %s: %w", entry.Name(), decodeErr))
			continue
		}
		report, decodeErr := record.terminalReport()
		if decodeErr != nil {
			errs = append(errs, fmt.Errorf("validate %s: %w", entry.Name(), decodeErr))
			continue
		}
		if want := terminalReportFileName(report.storageKey()); entry.Name() != want {
			errs = append(errs, fmt.Errorf("terminal report %s does not match task id", entry.Name()))
			continue
		}
		// In account-aware mode a record whose account does not match the
		// current account (a different account's record, or a legacy v1 record
		// written before an account was resolved) is preserved on disk but not
		// adopted for upload, so a login change cannot silently replay the
		// other account's pending terminal report.
		if s.accountAware && report.accountID != s.account {
			continue
		}
		items = append(items, pendingTerminalTaskReport{fileName: entry.Name(), report: report})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].fileName < items[j].fileName })
	return items, errors.Join(errs...)
}

// recoverTempFiles closes the atomic-write crash window after the temp file is
// fully flushed but before its rename. A partial/corrupt temp file is retained
// for forensic recovery and reported as an error; silently deleting it could
// discard the only copy of a terminal payload.
func (s *terminalReportStore) recoverTempFiles(entries []os.DirEntry) error {
	var errs []error
	changed := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		tempPath := filepath.Join(s.dir, name)
		body, err := os.ReadFile(tempPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("read interrupted terminal report %s: %w", name, err))
			continue
		}
		record, err := decodePersistedTerminalReport(body)
		if err != nil {
			errs = append(errs, fmt.Errorf("decode interrupted terminal report %s: %w", name, err))
			continue
		}
		report, err := record.terminalReport()
		if err != nil {
			errs = append(errs, fmt.Errorf("validate interrupted terminal report %s: %w", name, err))
			continue
		}
		targetName := terminalReportFileName(report.storageKey())
		wantPrefix := "." + strings.TrimSuffix(targetName, ".json") + "-"
		if !strings.HasPrefix(name, wantPrefix) {
			errs = append(errs, fmt.Errorf("interrupted terminal report %s does not match task id", name))
			continue
		}
		targetPath := filepath.Join(s.dir, targetName)
		if existingBody, readErr := os.ReadFile(targetPath); readErr == nil {
			existing, decodeErr := decodePersistedTerminalReport(existingBody)
			if decodeErr != nil {
				errs = append(errs, fmt.Errorf("decode terminal report while recovering %s: %w", targetName, decodeErr))
				continue
			}
			existingReport, decodeErr := existing.terminalReport()
			if decodeErr != nil || !terminalReportsEqual(existingReport, report) {
				errs = append(errs, fmt.Errorf("interrupted terminal report %s conflicts with existing payload", name))
				continue
			}
			if err := os.Remove(tempPath); err != nil {
				errs = append(errs, fmt.Errorf("remove duplicate interrupted terminal report %s: %w", name, err))
				continue
			}
			changed = true
			continue
		} else if !errors.Is(readErr, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("inspect terminal report while recovering %s: %w", targetName, readErr))
			continue
		}
		if err := os.Chmod(tempPath, 0o600); err != nil {
			errs = append(errs, fmt.Errorf("secure interrupted terminal report %s: %w", name, err))
			continue
		}
		if err := os.Rename(tempPath, targetPath); err != nil {
			errs = append(errs, fmt.Errorf("recover interrupted terminal report %s: %w", name, err))
			continue
		}
		changed = true
	}
	if changed {
		if err := syncTerminalReportDir(s.dir); err != nil {
			errs = append(errs, fmt.Errorf("sync terminal report queue after recovery: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (s *terminalReportStore) acknowledge(item pendingTerminalTaskReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.fileName != terminalReportFileName(item.report.storageKey()) {
		return errors.New("terminal report acknowledgement does not match task id")
	}
	path := filepath.Join(s.dir, item.fileName)
	if body, err := os.ReadFile(path); err == nil {
		current, err := decodePersistedTerminalReport(body)
		if err != nil {
			return err
		}
		report, err := current.terminalReport()
		if err != nil {
			return err
		}
		if !terminalReportsEqual(report, item.report) {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove acknowledged terminal report: %w", err)
	}
	if err := syncTerminalReportDir(s.dir); err != nil {
		return fmt.Errorf("sync terminal report queue after acknowledgement: %w", err)
	}
	return nil
}

// terminalReportPermanentRejection classifies only response semantics that are
// stable for an unchanged terminal request. Authentication expiry (401), rate
// limiting (429), and timeout (408) stay pending because credentials or
// deployment version can recover, as do generic and missing-route 404s.
//
// A conflict (409) is permanent only for a terminal task result: /complete and
// /fail return 409 solely for service.ErrExecutionStale (a revoked or mismatched
// execution fence). That state can never recover for an unchanged payload and
// credential, so such a result is quarantined instead of replayed forever.
// Other 409s are transient (for example a shared application instance still in
// use) and stay pending.
func terminalReportPermanentRejection(kind terminalTaskReportKind, err error) (int, bool) {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return 0, false
	}
	switch reqErr.StatusCode {
	case http.StatusBadRequest, http.StatusForbidden:
		return reqErr.StatusCode, true
	case http.StatusNotFound:
		return reqErr.StatusCode, isTaskNotFoundError(err)
	case http.StatusConflict:
		// Only a terminal task result's 409 (an ErrExecutionStale fence) is
		// permanent; a shared application instance can still be in use.
		if kind == terminalTaskReportComplete || kind == terminalTaskReportFail {
			return reqErr.StatusCode, true
		}
	}
	return 0, false
}

// recordPermanentRejection durably counts explicit server rejections and moves
// the unchanged original payload to failed/ once both the count and age gates
// are met. The failed record is intentionally retained indefinitely: automatic
// TTL/size eviction would silently discard the result this outbox protects.
func (s *terminalReportStore) recordPermanentRejection(item pendingTerminalTaskReport, status int, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDir(); err != nil {
		return false, err
	}
	path := filepath.Join(s.dir, item.fileName)
	body, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read rejected terminal report: %w", err)
	}
	record, err := decodePersistedTerminalReport(body)
	if err != nil {
		return false, fmt.Errorf("decode rejected terminal report: %w", err)
	}
	report, err := record.terminalReport()
	if err != nil {
		return false, fmt.Errorf("validate rejected terminal report: %w", err)
	}
	if item.fileName != terminalReportFileName(report.storageKey()) || !terminalReportsEqual(report, item.report) {
		return false, errors.New("rejected terminal report no longer matches queued payload")
	}

	now = now.UTC()
	if record.FirstPermanentRejectionAt == nil {
		first := now
		record.FirstPermanentRejectionAt = &first
	}
	last := now
	record.LastPermanentRejectionAt = &last
	record.LastPermanentStatus = status
	record.PermanentRejectionCount++
	age := now.Sub(*record.FirstPermanentRejectionAt)
	quarantine := record.PermanentRejectionCount >= terminalReportPermanentRejectionLimit && age >= terminalReportPermanentRejectionAge
	if quarantine {
		quarantinedAt := now
		record.QuarantinedAt = &quarantinedAt
	}
	if err := writeTerminalReportRecord(s.dir, item.fileName, record); err != nil {
		return false, fmt.Errorf("persist terminal report rejection: %w", err)
	}
	if !quarantine {
		return false, nil
	}
	if err := ensureTerminalReportDir(s.failedDir()); err != nil {
		return false, fmt.Errorf("create failed terminal report queue: %w", err)
	}
	failedPath := filepath.Join(s.failedDir(), item.fileName)
	// A reclaimed task can produce another terminal payload after an earlier
	// one was quarantined. Preserve both instead of blocking replay forever.
	payload, err := terminalReportPayload(report)
	if err != nil {
		return false, fmt.Errorf("encode quarantined terminal payload: %w", err)
	}
	payloadSum := sha256.Sum256(payload)
	alternatePath := filepath.Join(s.failedDir(), strings.TrimSuffix(item.fileName, ".json")+"-"+hex.EncodeToString(payloadSum[:])+".json")
	for {
		if existingBody, readErr := os.ReadFile(failedPath); readErr == nil {
			existing, decodeErr := decodePersistedTerminalReport(existingBody)
			var existingReport terminalTaskReport
			if decodeErr == nil {
				existingReport, decodeErr = existing.terminalReport()
			}
			if decodeErr != nil || !terminalReportsEqual(existingReport, report) {
				if failedPath == alternatePath {
					return false, errors.New("quarantined terminal payload fingerprint conflicts with existing evidence")
				}
				failedPath = alternatePath
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, fmt.Errorf("remove duplicate quarantined terminal report: %w", err)
			}
		} else if errors.Is(readErr, os.ErrNotExist) {
			if err := os.Rename(path, failedPath); err != nil {
				return false, fmt.Errorf("quarantine terminal report: %w", err)
			}
		} else {
			return false, fmt.Errorf("inspect failed terminal report: %w", readErr)
		}
		break
	}
	// The rename/removal has completed at this point. Report sync failures to
	// operators, but also return quarantined=true so the caller performs the
	// one-time server compensation. Returning false would leave no pending file
	// for a later pass to rediscover and could strand the server row in running.
	failedSyncErr := syncTerminalReportDir(s.failedDir())
	pendingSyncErr := syncTerminalReportDir(s.dir)
	return true, errors.Join(
		wrapTerminalReportSyncError("sync failed terminal report queue", failedSyncErr),
		wrapTerminalReportSyncError("sync pending terminal report queue after quarantine", pendingSyncErr),
	)
}

func wrapTerminalReportSyncError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func terminalReportDirectoryStats(dir string) (count int, bytes int64, err error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			errs = append(errs, fmt.Errorf("stat %s: %w", entry.Name(), infoErr))
			continue
		}
		count++
		bytes += info.Size()
	}
	return count, bytes, errors.Join(errs...)
}

func (s *terminalReportStore) stats() (terminalReportStoreStats, error) {
	if s == nil {
		return terminalReportStoreStats{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var stats terminalReportStoreStats
	var errs []error
	if count, bytes, err := terminalReportDirectoryStats(s.dir); err != nil {
		errs = append(errs, fmt.Errorf("scan pending terminal reports: %w", err))
	} else {
		stats.PendingCount, stats.PendingBytes = count, bytes
	}
	if count, bytes, err := terminalReportDirectoryStats(s.failedDir()); err != nil {
		errs = append(errs, fmt.Errorf("scan failed terminal reports: %w", err))
	} else {
		stats.FailedCount, stats.FailedBytes = count, bytes
	}
	return stats, errors.Join(errs...)
}

// otherNamespaceStats surfaces records owned by a different server/profile/
// daemon identity. Replaying them with the current credential could cross an
// account boundary, so startup warns instead of adopting or deleting them.
func (s *terminalReportStore) otherNamespaceStats() ([]terminalReportNamespaceStats, error) {
	if s == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan terminal report namespaces: %w", err)
	}
	var out []terminalReportNamespaceStats
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == s.namespace {
			continue
		}
		dir := filepath.Join(s.root, entry.Name())
		pendingCount, pendingBytes, pendingErr := terminalReportDirectoryStats(dir)
		failedCount, failedBytes, failedErr := terminalReportDirectoryStats(filepath.Join(dir, "failed"))
		if pendingErr != nil || failedErr != nil {
			errs = append(errs, fmt.Errorf("scan terminal report namespace %s: %w", entry.Name(), errors.Join(pendingErr, failedErr)))
		}
		stats := terminalReportStoreStats{
			PendingCount: pendingCount,
			PendingBytes: pendingBytes,
			FailedCount:  failedCount,
			FailedBytes:  failedBytes,
		}
		if stats.PendingCount+stats.FailedCount > 0 {
			out = append(out, terminalReportNamespaceStats{name: entry.Name(), stats: stats})
		}
	}
	return out, errors.Join(errs...)
}

func (d *Daemon) signalTerminalReportReplay() {
	if d.terminalReportWakeup == nil {
		return
	}
	select {
	case d.terminalReportWakeup <- struct{}{}:
	default:
	}
}

// handleTerminalReportDeliveryError records an explicit permanent rejection.
// It returns true only after the original payload has been moved out of the
// replay set into failed/. Transient failures and early permanent rejections
// remain pending; a post-rename sync error is logged while compensation still
// runs because there is no pending path left for a later pass to discover.
func (d *Daemon) handleTerminalReportDeliveryError(ctx context.Context, item pendingTerminalTaskReport, deliveryErr error) bool {
	status, permanent := terminalReportPermanentRejection(item.report.kind, deliveryErr)
	if !permanent || d.terminalReports == nil {
		return false
	}
	quarantined, err := d.terminalReports.recordPermanentRejection(item, status, d.reportClock())
	if err != nil {
		d.logger.Error("record permanent terminal report rejection",
			"task", item.report.taskID,
			"kind", item.report.kind,
			"status", status,
			"error", err,
		)
	}
	if !quarantined {
		return false
	}
	d.logger.Error("terminal report permanently rejected and quarantined",
		"task", item.report.taskID,
		"kind", item.report.kind,
		"status", status,
		"failed_queue", true,
	)

	// A rejected success can otherwise leave a server row in running forever.
	// Preserve the complete payload in failed/ first, then make the legacy fail
	// compensation as a separate request. It can settle the row but can never
	// overwrite or delete the user's original successful result. A semantic
	// task-not-found response needs no compensation because no row remains.
	if item.report.kind == terminalTaskReportComplete && !isTaskNotFoundError(deliveryErr) {
		fallback := terminalTaskReport{
			kind:                  terminalTaskReportFail,
			taskID:                item.report.taskID,
			errorMessage:          fmt.Sprintf("successful terminal result was rejected by the server with HTTP %d; the original completion is preserved in the daemon failed terminal-report queue", status),
			branchName:            item.report.branchName,
			sessionID:             item.report.sessionID,
			workDir:               item.report.workDir,
			durableWorkDir:        item.report.durableWorkDir,
			failureReason:         "agent_error.unknown",
			sessionRolloutMissing: item.report.sessionRolloutMissing,
			retiredSessionID:      item.report.retiredSessionID,
			execution:             item.report.execution,
			accountID:             item.report.accountID,
			executionID:           item.report.executionID,
		}
		if err := d.sendTerminalTaskReport(ctx, fallback, nil); err != nil {
			if queueErr := d.queueTaskReport(fallback); queueErr != nil {
				d.logger.Error("persist quarantine compensation", "task", fallback.taskID, "error", queueErr)
			}
			d.logger.Error("terminal report quarantine failure compensation was not accepted",
				"task", item.report.taskID,
				"error", err,
			)
		} else {
			d.logger.Warn("terminal report quarantine settled server task as failed; original completion retained",
				"task", item.report.taskID,
			)
		}
	}
	return true
}
