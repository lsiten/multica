package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestEnvironmentPolicyPersistsOnlyWithinOwningScope(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	scope := environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime"}
	policy := d.defaultEnvironmentPolicy()
	policy.Enabled, policy.ArchiveAfterHours = false, 72
	if err := d.saveEnvironmentPolicy(scope, policy); err != nil {
		t.Fatal(err)
	}
	loaded, err := d.loadEnvironmentPolicy(scope)
	if err != nil || loaded != policy {
		t.Fatalf("policy not persisted: %+v %v", loaded, err)
	}
	other, err := d.loadEnvironmentPolicy(environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "other-runtime"})
	if err != nil || other == policy {
		t.Fatalf("policy crossed runtime: %+v %v", other, err)
	}
	if err := d.saveEnvironmentPolicy(environmentOperationScope{WorkspaceID: "ws2", RuntimeID: "runtime"}, policy); err == nil {
		t.Fatal("foreign workspace policy accepted")
	}
	policy.ArchiveAfterHours = -1
	if err := d.saveEnvironmentPolicy(scope, policy); err == nil {
		t.Fatal("invalid retention accepted")
	}
	d.cfg.Profile = "different-profile"
	if loaded, err := d.loadEnvironmentPolicy(scope); err != nil || !loaded.Enabled {
		t.Fatalf("policy crossed profile: %+v %v", loaded, err)
	}
}

func TestRemotePolicyUpdateUsesOwningRuntimeAndPreservesOtherRuntimeDefaults(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	policy := d.defaultEnvironmentPolicy()
	policy.ArchiveAfterHours = 96
	command := protocol.LocalReviewCommand{Action: "environment", WorkspaceID: "ws1", RuntimeID: "runtime", ActorID: "owner", Environment: &protocol.EnvironmentCommand{Action: "policy_update", Policy: &policy}}
	result := d.runRemoteEnvironment(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	other, err := d.environmentPolicyStatus(environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "other-runtime"})
	if err != nil || other.Policy.ArchiveAfterHours == 96 {
		t.Fatalf("policy update crossed runtime: %+v %v", other, err)
	}
	command.WorkspaceID = "ws2"
	if result := d.runRemoteEnvironment(t.Context(), command); result.Error == "" {
		t.Fatal("foreign workspace updated policy")
	}
}

func TestCapacityPressureReclaimsVerifiedCacheWithReceiptAndRetainsOutputs(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	d.cfg.GCEnabled, d.cfg.EnvironmentRecycleEnabled = true, true
	scope := environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime"}
	policy := d.defaultEnvironmentPolicy()
	policy.CacheAfterHours, policy.PressureCacheAfterHours, policy.MaxDirectoryBytes = 12, 1, 1
	if err := d.saveEnvironmentPolicy(scope, policy); err != nil {
		t.Fatal(err)
	}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", &execenv.GCMeta{Kind: execenv.GCKindIssue, WorkspaceID: "ws1", TaskID: "task", CompletedAt: time.Now().Add(-2 * time.Hour)})
	cache := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "binary")
	output := filepath.Join(root, "output", "deliverable")
	writeLifecycleFile(t, cache, "regenerable cache")
	writeLifecycleFile(t, output, "keep deliverable")
	d.scanEnvironmentCapacity(t.Context())
	d.environmentOperationWorkers.Wait()
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("pressure cache remains: %v", err)
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "keep deliverable" {
		t.Fatalf("output removed: %q %v", data, err)
	}
	rows, err := d.listEnvironmentOperations(scope)
	if err != nil || len(rows) != 1 || rows[0].Action != "clean_cache" || !rows[0].Automatic || len(rows[0].Results) != 1 {
		t.Fatalf("capacity receipt missing: %+v %v", rows, err)
	}
	status, err := d.environmentPolicyStatus(scope)
	if err != nil || !status.UnderPressure || status.LastScanAt == nil || status.IdleEnvironments != 1 {
		t.Fatalf("capacity status missing: %+v %v", status, err)
	}
}

func TestAutomaticCacheRechecksDisabledPolicyAfterPreview(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	d.cfg.GCEnabled, d.cfg.EnvironmentRecycleEnabled = true, true
	scope := environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime", Automatic: true}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", &execenv.GCMeta{Kind: execenv.GCKindIssue, WorkspaceID: "ws1", TaskID: "task", CompletedAt: time.Now().Add(-48 * time.Hour)})
	cache := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "binary")
	writeLifecycleFile(t, cache, "keep cache")
	preview := d.worktreeCacheOperation(t.Context(), root, "")
	policy := d.defaultEnvironmentPolicy()
	policy.Enabled = false
	if err := d.saveEnvironmentPolicy(scope, policy); err != nil {
		t.Fatal(err)
	}
	result := d.worktreeCacheOperation(withEnvironmentScope(t.Context(), scope), root, preview.Revision)
	if result.Reason != "disabled" || result.RemovedCount != 0 {
		t.Fatalf("disabled policy still cleaned: %+v", result)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal("cache removed despite disabled policy")
	}
	meta, err := execenv.ReadGCMeta(root)
	if err != nil {
		t.Fatal(err)
	}
	if eligible, reason := d.automaticCleanupEligible(t.Context(), root, meta); eligible || reason != "disabled" {
		t.Fatalf("disabled policy still permits automatic archive: %v %s", eligible, reason)
	}
}

func TestEnvironmentPressureThresholdsDoNotRequireKnownDiskFree(t *testing.T) {
	policy := protocol.EnvironmentPolicy{MaxIdleEnvironments: 10, MaxDirectoryBytes: 100, MinimumFreeBytes: 20}
	free := uint64(19)
	if !environmentUnderPressure(policy, 1, 1, &free) || !environmentUnderPressure(policy, 11, 1, nil) || !environmentUnderPressure(policy, 1, 101, nil) || environmentUnderPressure(policy, 10, 100, nil) {
		t.Fatal("pressure thresholds did not preserve independent triggers")
	}
}
