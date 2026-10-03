package protocol

import (
	"strings"
	"testing"
)

func TestEnvironmentCommandRejectsPathsAndAmbiguousMutations(t *testing.T) {
	for _, command := range []EnvironmentCommand{
		{Action: "inventory", OperationID: strings.Repeat("a", 64)},
		{Action: "operation_start"},
		{Action: "operation_status", OperationID: "/tmp/root"},
		{Action: "operation_start", Operation: &EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "clean_cache", Selections: []EnvironmentSelection{{EnvironmentID: strings.Repeat("b", 64)}}}},
		{Action: "operation_start", Operation: &EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "restore", ArchiveID: "/tmp/root"}},
	} {
		if command.Validate() == nil {
			t.Fatalf("invalid command accepted: %+v", command)
		}
	}
	valid := EnvironmentCommand{Action: "operation_start", Operation: &EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "clean_cache", Selections: []EnvironmentSelection{{EnvironmentID: strings.Repeat("b", 64), Revision: strings.Repeat("c", 64)}}}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentPolicyCommandsRequireBoundedExclusiveInput(t *testing.T) {
	policy := EnvironmentPolicy{Enabled: true, ArchiveAfterHours: 24, CacheAfterHours: 12, PressureCacheAfterHours: 1, MaxIdleEnvironments: 100, MaxDirectoryBytes: 20 << 30, MinimumFreeBytes: 5 << 30}
	if err := (EnvironmentCommand{Action: "policy_update", Policy: &policy}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (EnvironmentCommand{Action: "policy"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, command := range []EnvironmentCommand{{Action: "policy", Policy: &policy}, {Action: "policy_update"}, {Action: "policy_update", Policy: &policy, OperationID: strings.Repeat("a", 64)}} {
		if command.Validate() == nil {
			t.Fatalf("ambiguous policy command accepted: %+v", command)
		}
	}
	policy.MinimumFreeBytes = -1
	if policy.Validate() == nil {
		t.Fatal("negative disk threshold accepted")
	}
}
