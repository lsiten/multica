package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentPolicyRecord struct {
	WorkspaceID, RuntimeID, Profile, BackendURL, DaemonID string
	Policy                                                protocol.EnvironmentPolicy
}

func (d *Daemon) automaticCacheEligible(ctx context.Context, path string, owner *execenv.EnvRootOwner, status *protocol.TaskGCStatus) (bool, string) {
	if !d.cfg.GCEnabled || !d.cfg.EnvironmentRecycleEnabled || d.cfg.KeepEnvAfterTask {
		return false, "disabled"
	}
	scope := environmentOperationScope{WorkspaceID: owner.WorkspaceID, RuntimeID: status.RuntimeID}
	policy, err := d.loadEnvironmentPolicy(scope)
	if err != nil {
		return false, "policy_unavailable"
	}
	if !policy.Enabled {
		return false, "disabled"
	}
	meta, err := execenv.ReadGCMeta(path)
	if err != nil || meta.CompletedAt.IsZero() {
		return false, "unknown_completion"
	}
	completed := meta.CompletedAt
	if status.CompletedAt.After(completed) {
		completed = status.CompletedAt
	}
	free, _ := environmentFreeBytes(d.cfg.WorkspacesRoot)
	d.environmentPolicyMu.Lock()
	scan := d.environmentPolicyScans[d.environmentOperationKey(scope, "policy")]
	d.environmentPolicyMu.Unlock()
	hours := policy.CacheAfterHours
	if environmentUnderPressure(policy, scan.IdleEnvironments, scan.DirectoryBytes, free) && policy.PressureCacheAfterHours < hours {
		hours = policy.PressureCacheAfterHours
	}
	if hours <= 0 || time.Since(completed) < time.Duration(hours)*time.Hour {
		return false, "retention"
	}
	return true, ""
}

func (d *Daemon) defaultEnvironmentPolicy() protocol.EnvironmentPolicy {
	hours := func(duration time.Duration) int {
		value := duration / time.Hour
		if duration%time.Hour > 0 {
			value++
		}
		return int(value)
	}
	return protocol.EnvironmentPolicy{Enabled: true, ArchiveAfterHours: hours(d.cfg.EnvironmentArchiveTTL), CacheAfterHours: hours(d.cfg.GCArtifactTTL), PressureCacheAfterHours: 1, MaxIdleEnvironments: 100, MaxDirectoryBytes: 20 << 30, MinimumFreeBytes: 5 << 30}
}

func (d *Daemon) environmentPolicyDirectory() (*os.Root, error) {
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.Mkdir(".environment-policies", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := root.Lstat(".environment-policies")
	if err != nil || !info.IsDir() || info.Mode()&linkedDirModes != 0 {
		return nil, errors.New("policy directory unavailable")
	}
	return root.OpenRoot(".environment-policies")
}

func (d *Daemon) loadEnvironmentPolicy(scope environmentOperationScope) (protocol.EnvironmentPolicy, error) {
	d.environmentPolicyMu.Lock()
	defer d.environmentPolicyMu.Unlock()
	return d.loadEnvironmentPolicyLocked(scope)
}

func (d *Daemon) loadEnvironmentPolicyLocked(scope environmentOperationScope) (protocol.EnvironmentPolicy, error) {
	policy := d.defaultEnvironmentPolicy()
	root, err := d.environmentPolicyDirectory()
	if err != nil {
		return policy, err
	}
	defer root.Close()
	name := d.environmentOperationKey(scope, "policy") + ".json"
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return policy, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return policy, errors.New("invalid environment policy file")
	}
	data, err := root.ReadFile(name)
	if err != nil {
		return policy, err
	}
	var record environmentPolicyRecord
	if json.Unmarshal(data, &record) != nil || record.WorkspaceID != scope.WorkspaceID || record.RuntimeID != scope.RuntimeID || record.Profile != d.cfg.Profile || record.BackendURL != d.cfg.ServerBaseURL || record.DaemonID != d.cfg.DaemonID || record.Policy.Validate() != nil {
		return policy, errors.New("environment policy scope mismatch")
	}
	return record.Policy, nil
}

func (d *Daemon) saveEnvironmentPolicy(scope environmentOperationScope, policy protocol.EnvironmentPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if !d.environmentRuntimeOwnedHere(scope) {
		return errors.New("runtime ownership changed")
	}
	d.environmentPolicyMu.Lock()
	defer d.environmentPolicyMu.Unlock()
	root, err := d.environmentPolicyDirectory()
	if err != nil {
		return err
	}
	defer root.Close()
	name := d.environmentOperationKey(scope, "policy") + ".json"
	data, err := json.Marshal(environmentPolicyRecord{scope.WorkspaceID, scope.RuntimeID, d.cfg.Profile, d.cfg.ServerBaseURL, d.cfg.DaemonID, policy})
	if err != nil {
		return err
	}
	file, err := root.OpenFile(name+".pending", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	return root.Rename(name+".pending", name)
}

func (d *Daemon) environmentPolicyStatus(scope environmentOperationScope) (protocol.EnvironmentPolicyStatus, error) {
	policy, err := d.loadEnvironmentPolicy(scope)
	if err != nil {
		return protocol.EnvironmentPolicyStatus{}, err
	}
	d.environmentPolicyMu.Lock()
	status := d.environmentPolicyScans[d.environmentOperationKey(scope, "policy")]
	d.environmentPolicyMu.Unlock()
	status.WorkspaceID, status.RuntimeID, status.Policy = scope.WorkspaceID, scope.RuntimeID, policy
	status.TaskRetentionSupported = true
	status.EffectiveEnabled = policy.Enabled && d.cfg.GCEnabled && d.cfg.EnvironmentRecycleEnabled && !d.cfg.KeepEnvAfterTask
	interval := d.cfg.EnvironmentRecycleInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	status.ScanIntervalSeconds = int64(interval / time.Second)
	status.FreeBytes, err = environmentFreeBytes(d.cfg.WorkspacesRoot)
	if err != nil {
		return status, err
	}
	status.UnderPressure = environmentUnderPressure(policy, status.IdleEnvironments, status.DirectoryBytes, status.FreeBytes)
	return status, nil
}

func environmentUnderPressure(policy protocol.EnvironmentPolicy, count int, bytes int64, free *uint64) bool {
	return policy.MaxIdleEnvironments > 0 && count > policy.MaxIdleEnvironments || policy.MaxDirectoryBytes > 0 && bytes > policy.MaxDirectoryBytes ||
		policy.MinimumFreeBytes > 0 && free != nil && *free < uint64(policy.MinimumFreeBytes)
}
