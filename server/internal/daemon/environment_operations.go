package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentOperationScope struct {
	WorkspaceID, RuntimeID string
	Automatic              bool
}

type environmentOperationRecord struct {
	protocol.EnvironmentOperationStatus
	BackendURL string `json:"backend_url"`
	InputHash  string `json:"input_hash"`
}

type environmentOperationLive struct {
	record environmentOperationRecord
	cancel context.CancelFunc
}

func (d *Daemon) environmentOperationKey(scope environmentOperationScope, id string) string {
	digest := sha256.Sum256([]byte(d.cfg.ServerBaseURL + "\x00" + d.cfg.Profile + "\x00" + scope.WorkspaceID + "\x00" + scope.RuntimeID + "\x00" + id))
	return hex.EncodeToString(digest[:])
}

func (d *Daemon) writeEnvironmentOperation(scope environmentOperationScope, record environmentOperationRecord) error {
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Mkdir(".environment-operations", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := root.Lstat(".environment-operations")
	if err != nil || !info.IsDir() || info.Mode()&linkedDirModes != 0 {
		return errors.New("operation ledger unavailable")
	}
	ledger, err := root.OpenRoot(".environment-operations")
	if err != nil {
		return err
	}
	defer ledger.Close()
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	name := d.environmentOperationKey(scope, record.ID) + ".json"
	temporary := name + ".pending"
	file, err := ledger.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	return ledger.Rename(temporary, name)
}

func (d *Daemon) readEnvironmentOperation(scope environmentOperationScope, id string) (environmentOperationRecord, error) {
	var record environmentOperationRecord
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return record, err
	}
	defer root.Close()
	name := filepath.Join(".environment-operations", d.environmentOperationKey(scope, id)+".json")
	info, err := root.Lstat(name)
	if err != nil {
		return record, err
	}
	if !info.Mode().IsRegular() || info.Size() > 12<<20 {
		return record, errors.New("invalid operation receipt")
	}
	data, err := root.ReadFile(name)
	if err != nil {
		return record, err
	}
	if json.Unmarshal(data, &record) != nil || record.ID != id || record.WorkspaceID != scope.WorkspaceID || record.RuntimeID != scope.RuntimeID || record.Profile != d.cfg.Profile || record.BackendURL != d.cfg.ServerBaseURL || record.DaemonID != d.cfg.DaemonID {
		return record, errors.New("operation scope mismatch")
	}
	return record, nil
}

func cloneEnvironmentOperation(record environmentOperationRecord) protocol.EnvironmentOperationStatus {
	result := record.EnvironmentOperationStatus
	result.Results = append([]json.RawMessage{}, result.Results...)
	return result
}

func (d *Daemon) startEnvironmentOperation(scope environmentOperationScope, request protocol.EnvironmentOperationRequest) (protocol.EnvironmentOperationStatus, error) {
	if err := request.Validate(); err != nil {
		return protocol.EnvironmentOperationStatus{}, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return protocol.EnvironmentOperationStatus{}, err
	}
	digest := sha256.Sum256(data)
	inputHash := hex.EncodeToString(digest[:])
	key := d.environmentOperationKey(scope, request.ID)
	d.environmentOperationsMu.Lock()
	defer d.environmentOperationsMu.Unlock()
	if d.environmentOperationsStopped || d.recoveryContext().Err() != nil {
		return protocol.EnvironmentOperationStatus{}, errors.New("daemon is stopping")
	}
	if live := d.environmentOperations[key]; live != nil {
		if live.record.InputHash != inputHash {
			return protocol.EnvironmentOperationStatus{}, errors.New("operation identity already used for different input")
		}
		return cloneEnvironmentOperation(live.record), nil
	}
	if previous, err := d.readEnvironmentOperation(scope, request.ID); err == nil {
		if previous.InputHash != inputHash {
			return protocol.EnvironmentOperationStatus{}, errors.New("operation identity already used for different input")
		}
		if previous.Status == "running" || previous.Status == "cancelling" {
			previous.Status = "interrupted"
			previous.Error = "daemon restarted; inspect partial results before starting another operation"
		}
		return cloneEnvironmentOperation(previous), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return protocol.EnvironmentOperationStatus{}, err
	}
	active, automaticActive := 0, 0
	for _, live := range d.environmentOperations {
		if live.cancel != nil {
			active++
			if live.record.Automatic {
				automaticActive++
			}
		}
	}
	if scope.Automatic && automaticActive >= 2 {
		return protocol.EnvironmentOperationStatus{}, errors.New("automatic environment operation capacity reached")
	}
	if active >= 16 {
		return protocol.EnvironmentOperationStatus{}, errors.New("environment operation capacity reached")
	}
	if d.environmentOperations == nil {
		d.environmentOperations = make(map[string]*environmentOperationLive)
	}
	now := time.Now().UTC()
	total := len(request.Selections)
	if request.Action == "restore" {
		total = 1
	}
	status := protocol.EnvironmentOperationStatus{ID: request.ID, Action: request.Action, Status: "running", Automatic: scope.Automatic, WorkspaceID: scope.WorkspaceID, RuntimeID: scope.RuntimeID, DaemonID: d.cfg.DaemonID, Profile: d.cfg.Profile, StartedAt: now, UpdatedAt: now, Total: total, Results: []json.RawMessage{}}
	live := &environmentOperationLive{record: environmentOperationRecord{EnvironmentOperationStatus: status, BackendURL: d.cfg.ServerBaseURL, InputHash: inputHash}}
	if err := d.writeEnvironmentOperation(scope, live.record); err != nil {
		return protocol.EnvironmentOperationStatus{}, err
	}
	ctx, cancel := context.WithTimeout(d.recoveryContext(), 30*time.Minute)
	live.cancel = cancel
	d.environmentOperations[key] = live
	d.environmentOperationWorkers.Go(func() { defer cancel(); d.runEnvironmentOperation(ctx, scope, key, request) })
	return cloneEnvironmentOperation(live.record), nil
}

func (d *Daemon) automaticEnvironmentCapacityAvailable() bool {
	d.environmentOperationsMu.Lock()
	defer d.environmentOperationsMu.Unlock()
	if d.environmentOperationsStopped {
		return false
	}
	active := 0
	for _, live := range d.environmentOperations {
		if live.cancel != nil && live.record.Automatic {
			active++
		}
	}
	return active < 2
}

func (d *Daemon) environmentOperationStatus(scope environmentOperationScope, id string, cancel bool) (protocol.EnvironmentOperationStatus, error) {
	if !protocol.ValidEnvironmentIdentity(id) {
		return protocol.EnvironmentOperationStatus{}, errors.New("invalid operation identity")
	}
	key := d.environmentOperationKey(scope, id)
	d.environmentOperationsMu.Lock()
	defer d.environmentOperationsMu.Unlock()
	if live := d.environmentOperations[key]; live != nil {
		if cancel && live.cancel != nil {
			live.cancel()
			live.record.Status = "cancelling"
			live.record.UpdatedAt = time.Now().UTC()
			if err := d.writeEnvironmentOperation(scope, live.record); err != nil {
				return protocol.EnvironmentOperationStatus{}, err
			}
		}
		return cloneEnvironmentOperation(live.record), nil
	}
	record, err := d.readEnvironmentOperation(scope, id)
	if err != nil {
		return protocol.EnvironmentOperationStatus{}, err
	}
	if record.Status == "running" || record.Status == "cancelling" {
		record.Status = "interrupted"
		record.Error = "daemon restarted; inspect partial results before starting another operation"
	}
	return cloneEnvironmentOperation(record), nil
}

func (d *Daemon) updateEnvironmentOperation(scope environmentOperationScope, key string, result json.RawMessage, state, failure string) error {
	d.environmentOperationsMu.Lock()
	defer d.environmentOperationsMu.Unlock()
	live := d.environmentOperations[key]
	if result != nil {
		live.record.Results = append(live.record.Results, result)
		live.record.Completed++
	}
	if state != "" {
		live.record.Status = state
		live.record.Error = failure
		live.cancel = nil
	}
	live.record.UpdatedAt = time.Now().UTC()
	err := d.writeEnvironmentOperation(scope, live.record)
	if err == nil && state != "" {
		delete(d.environmentOperations, key)
	}
	return err
}

func (d *Daemon) runEnvironmentOperation(ctx context.Context, scope environmentOperationScope, key string, request protocol.EnvironmentOperationRequest) {
	complete := func(state, failure string) {
		if err := d.updateEnvironmentOperation(scope, key, nil, state, failure); err != nil {
			d.logger.Error("environment operation receipt failed", "error", err)
		}
	}
	if request.Action == "restore" {
		result, err := d.runScopedEnvironmentRestore(ctx, scope, request.ArchiveID)
		if err != nil {
			complete("failed", err.Error())
			return
		}
		data, err := json.Marshal(result)
		if err != nil {
			complete("failed", "result encoding failed")
			return
		}
		if err := d.updateEnvironmentOperation(scope, key, data, "", ""); err != nil {
			complete("failed", "receipt persistence failed")
			return
		}
		if ctx.Err() != nil {
			complete("cancelled", "operation cancelled")
			return
		}
		complete("completed", "")
		return
	}
	known, err := d.scopedEnvironmentPaths(ctx, scope)
	if err != nil {
		if ctx.Err() != nil {
			complete("cancelled", "operation cancelled")
			return
		}
		complete("failed", err.Error())
		return
	}
	for _, selection := range request.Selections {
		if ctx.Err() != nil {
			complete("cancelled", "operation cancelled")
			return
		}
		var result any
		path, exists := known[selection.EnvironmentID]
		if !exists {
			result = map[string]any{"environment_id": selection.EnvironmentID, "reason": "unowned"}
		} else {
			result, err = d.runScopedEnvironmentMutation(ctx, scope, request, selection, path)
			if err != nil {
				result = map[string]any{"environment_id": selection.EnvironmentID, "reason": "scope_changed"}
			}
		}
		data, err := json.Marshal(result)
		if err != nil {
			complete("failed", "result encoding failed")
			return
		}
		if err := d.updateEnvironmentOperation(scope, key, data, "", ""); err != nil {
			complete("failed", "receipt persistence failed")
			return
		}
	}
	if ctx.Err() != nil {
		complete("cancelled", "operation cancelled")
		return
	}
	complete("completed", "")
}

func (d *Daemon) stopEnvironmentOperations() {
	d.environmentOperationsMu.Lock()
	d.environmentOperationsStopped = true
	for _, live := range d.environmentOperations {
		if live.cancel != nil {
			live.cancel()
		}
	}
	d.environmentOperationsMu.Unlock()
	d.environmentOperationWorkers.Wait()
}

func (d *Daemon) listEnvironmentOperations(scope environmentOperationScope) ([]protocol.EnvironmentOperationStatus, error) {
	rows := []protocol.EnvironmentOperationStatus{}
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(".environment-operations")
	if errors.Is(err, os.ErrNotExist) {
		return rows, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&linkedDirModes != 0 {
		return nil, errors.New("operation ledger unavailable")
	}
	ledger, err := root.OpenRoot(".environment-operations")
	if err != nil {
		return nil, err
	}
	defer ledger.Close()
	entries, err := fs.ReadDir(ledger.FS(), ".")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&linkedDirModes != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 12<<20 {
			continue
		}
		data, err := ledger.ReadFile(entry.Name())
		if err != nil {
			continue
		}
		var record environmentOperationRecord
		if json.Unmarshal(data, &record) != nil || record.WorkspaceID != scope.WorkspaceID || record.RuntimeID != scope.RuntimeID || record.Profile != d.cfg.Profile || record.BackendURL != d.cfg.ServerBaseURL || record.DaemonID != d.cfg.DaemonID {
			continue
		}
		status, err := d.environmentOperationStatus(scope, record.ID, false)
		if err == nil {
			rows = append(rows, status)
		}
	}
	sort.Slice(rows, func(left, right int) bool { return rows[left].UpdatedAt.After(rows[right].UpdatedAt) })
	return rows[:min(100, len(rows))], nil
}
