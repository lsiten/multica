package jevmodels

import (
	"testing"
)

// TestLeaseCarriesNegotiatedExecutionIdentity proves that an acquire that binds
// a negotiated execution identity returns a lease carrying exactly that
// identity, so downstream JEV/gateway consumers can namespace by the execution.
func TestLeaseCarriesNegotiatedExecutionIdentity(t *testing.T) {
	m := newTestManager(t)
	fakeReady(m)

	lease, err := m.Acquire(t.Context(), Selection{
		ModelID:           ModelID,
		Device:            "cpu",
		ExecutionIdentity: "exec-negotiated-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.ExecutionIdentity != "exec-negotiated-1" {
		t.Fatalf("lease execution identity = %q, want exec-negotiated-1", lease.ExecutionIdentity)
	}
	lease.Release()
}

// TestLeaseDoesNotFabricateExecutionIdentity proves that a legacy or
// pre-existing acquire leaves the lease execution identity empty rather than
// inventing one, preserving the drain-only legacy lane.
func TestLeaseDoesNotFabricateExecutionIdentity(t *testing.T) {
	m := newTestManager(t)
	fakeReady(m)

	lease, err := m.Acquire(t.Context(), Selection{ModelID: ModelID, Device: "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	if lease.ExecutionIdentity != "" {
		t.Fatalf("lease execution identity = %q, want empty for legacy acquire", lease.ExecutionIdentity)
	}
	lease.Release()
}

// TestLeaseExecutionIdentityIsPerAcquire proves that the negotiated identity is
// bound to each acquired lease independently even when the leases share one
// ready host process, so concurrent executions cannot cross-namespace each
// other.
func TestLeaseExecutionIdentityIsPerAcquire(t *testing.T) {
	m := newTestManager(t)
	fakeReady(m)

	first, err := m.Acquire(t.Context(), Selection{
		ModelID:           ModelID,
		Device:            "cpu",
		ExecutionIdentity: "exec-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Acquire(t.Context(), Selection{
		ModelID:           ModelID,
		Device:            "cpu",
		ExecutionIdentity: "exec-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.process != second.process {
		t.Fatal("leases should share one ready host process")
	}
	if first.ExecutionIdentity != "exec-a" || second.ExecutionIdentity != "exec-b" {
		t.Fatalf("execution identities = %q / %q, want exec-a / exec-b", first.ExecutionIdentity, second.ExecutionIdentity)
	}
	// A short-lived third acquire without an identity must not adopt either.
	legacy, err := m.Acquire(t.Context(), Selection{ModelID: ModelID, Device: "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ExecutionIdentity != "" {
		t.Fatalf("legacy lease adopted execution identity %q", legacy.ExecutionIdentity)
	}
	legacy.Release()
	second.Release()
	first.Release()
}
