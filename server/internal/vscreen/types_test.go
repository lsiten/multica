package vscreen

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAppClaimSeparatesPIDIncarnationsAndLogin(t *testing.T) {
	c := AppClaim{Key: AppClaimKey{UID: 501, PID: 123, ProcessStartIdentity: "start-1"}, Resource: ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501}, Owner: NativeOwner{Profile: "dev", DaemonBootEpoch: "boot", NativeEpoch: "native"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	other := c.Key
	other.ProcessStartIdentity = "start-2"
	if other == c.Key {
		t.Fatal("reused PID inherited process identity")
	}
	c.Resource.UID++
	if err := c.Validate(); err == nil {
		t.Fatal("accepted cross-login process claim")
	}
	if err := (NativeOwner{}).Validate(); err == nil {
		t.Fatal("accepted empty native owner")
	}
}

func TestLeaseRejectsExpiredCancelledAndMissingAuthority(t *testing.T) {
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	lease := Lease{Resource: ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501}, TaskID: "task", TransactionID: "transaction", LeaseEpoch: 1, NativeEpoch: "native", ExpiresAt: now.Add(time.Second)}
	if err := lease.ValidateAt(now); err != nil {
		t.Fatal(err)
	}
	if err := lease.ValidateAt(lease.ExpiresAt); err == nil {
		t.Fatal("accepted expired lease at exact deadline")
	}
	lease.Cancelled = true
	if err := lease.ValidateAt(now); err == nil {
		t.Fatal("accepted cancelled lease")
	}
	if err := (Lease{}).ValidateAt(now); err == nil {
		t.Fatal("accepted zero lease")
	}
}

func TestErrorPreservesCauseWithoutLeakingIt(t *testing.T) {
	cause := errors.New("secret input")
	err := fmt.Errorf("dispatch: %w", &Error{Reason: protocol.VscreenActionUncertainReason, Cause: cause})
	if !errors.Is(err, cause) {
		t.Fatal("lost diagnostic cause")
	}
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Reason != protocol.VscreenActionUncertainReason {
		t.Fatal("lost typed refusal")
	}
	if err.Error() != "dispatch: vscreen: action_uncertain" {
		t.Fatalf("error leaked payload: %s", err)
	}
}
