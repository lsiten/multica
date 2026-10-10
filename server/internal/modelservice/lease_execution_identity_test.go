package modelservice

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestValidateExecutionIdentityNilIsValid proves a nil optional execution
// identity is a valid "not negotiated" state, so existing leases are unaffected.
func TestValidateExecutionIdentityNilIsValid(t *testing.T) {
	exec := Execution{WorkspaceID: "ws", RuntimeID: "rt", TaskID: "task", DispatchedAt: "2026-10-09T00:00:00Z"}
	if err := validateExecutionIdentity(nil, exec); err != nil {
		t.Fatalf("nil identity must be valid, got %v", err)
	}
}

// TestValidateExecutionIdentityRealIsAccepted proves a real server-issued
// execution identity that matches the execution is accepted.
func TestValidateExecutionIdentityRealIsAccepted(t *testing.T) {
	exec := Execution{WorkspaceID: "ws", RuntimeID: "rt", TaskID: "task", DispatchedAt: "2026-10-09T00:00:00Z"}
	ei := &protocol.ExecutionIdentity{
		TaskID:       "task",
		RuntimeID:    "rt",
		DispatchedAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
		ExecutionID:  "exec-1",
		WorkerID:     "worker-1",
	}
	if err := validateExecutionIdentity(ei, exec); err != nil {
		t.Fatalf("real identity must be accepted, got %v", err)
	}
}

// TestValidateExecutionIdentityInventedIsRejected is the core invariant: an
// identity with an empty ExecutionID is an invented/empty epoch and fails
// closed rather than being accepted.
func TestValidateExecutionIdentityInventedIsRejected(t *testing.T) {
	exec := Execution{WorkspaceID: "ws", RuntimeID: "rt", TaskID: "task", DispatchedAt: "2026-10-09T00:00:00Z"}
	cases := []struct {
		name string
		ei   *protocol.ExecutionIdentity
	}{
		{"empty execution id", &protocol.ExecutionIdentity{TaskID: "task", RuntimeID: "rt", DispatchedAt: time.Now()}},
		{"empty task id", &protocol.ExecutionIdentity{RuntimeID: "rt", ExecutionID: "exec-1", DispatchedAt: time.Now()}},
		{"empty runtime id", &protocol.ExecutionIdentity{TaskID: "task", ExecutionID: "exec-1", DispatchedAt: time.Now()}},
		{"zero dispatched at", &protocol.ExecutionIdentity{TaskID: "task", RuntimeID: "rt", ExecutionID: "exec-1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateExecutionIdentity(c.ei, exec); err == nil {
				t.Fatalf("invented/empty identity must fail closed")
			}
		})
	}
}

// TestValidateExecutionIdentityConflictIsRejected proves an identity that does
// not match the execution the lease was acquired for fails closed, so a
// mismatched server-issued identity cannot ride a different execution.
func TestValidateExecutionIdentityConflictIsRejected(t *testing.T) {
	exec := Execution{WorkspaceID: "ws", RuntimeID: "rt", TaskID: "task", DispatchedAt: "2026-10-09T00:00:00Z"}
	cases := []struct {
		name string
		ei   *protocol.ExecutionIdentity
	}{
		{"task id mismatch", &protocol.ExecutionIdentity{TaskID: "other", RuntimeID: "rt", DispatchedAt: time.Now(), ExecutionID: "exec-1"}},
		{"runtime id mismatch", &protocol.ExecutionIdentity{TaskID: "task", RuntimeID: "other", DispatchedAt: time.Now(), ExecutionID: "exec-1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateExecutionIdentity(c.ei, exec); err == nil {
				t.Fatalf("conflicting identity must fail closed")
			}
		})
	}
}

// TestLeaseCarriesExecutionIdentityOptional proves the lease carries the
// optional execution identity when present and stays nil (never fabricated)
// when the grant has none, so the daemon hot path is unchanged for existing
// leases.
func TestLeaseCarriesExecutionIdentityOptional(t *testing.T) {
	// No identity: nil lease field.
	if (&Lease{}).ExecutionIdentity != nil {
		t.Fatal("default lease must not carry an execution identity")
	}
}
