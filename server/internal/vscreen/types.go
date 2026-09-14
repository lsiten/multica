// Package vscreen owns runtime virtual-display identity and GUI control contracts.
package vscreen

import (
	"fmt"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"time"
)

// Lease is actor-owned transaction authority. ExpiresAt retains its monotonic
// clock locally; a wire timestamp is never restored as an active lease.
type Lease struct {
	Resource      ResourceKey
	TaskID        string
	TransactionID string
	LeaseEpoch    uint64
	NativeEpoch   string
	ExpiresAt     time.Time
	Cancelled     bool
}

// ValidateAt rejects missing, cancelled, or expired transaction authority.
// Callers supply the actor clock and still compare task and generation bindings.
func (l Lease) ValidateAt(now time.Time) error {
	if err := l.Resource.Validate(); err != nil {
		return err
	}
	if l.TaskID == "" || l.TransactionID == "" || l.LeaseEpoch == 0 || l.NativeEpoch == "" || now.IsZero() {
		return fmt.Errorf("%w: lease identity is incomplete", protocol.ErrInvalidVscreenContract)
	}
	if l.Cancelled || !l.ExpiresAt.After(now) {
		return &Error{Reason: protocol.VscreenLeaseExpired}
	}
	return nil
}

// ResourceKey is the shared backend/workspace/runtime/login display identity.
type ResourceKey = protocol.ResourceKey

// Epoch binds handles to a native host, display incarnation, and geometry revision.
type Epoch = protocol.VscreenEpoch

// NativeOwner distinguishes concurrent profiles and daemon/native restarts.
type NativeOwner struct {
	Profile         string `json:"profile"`
	DaemonBootEpoch string `json:"daemon_boot_epoch"`
	NativeEpoch     string `json:"native_epoch"`
}

// Validate rejects incomplete native ownership claims.
func (o NativeOwner) Validate() error {
	if o.Profile == "" || o.DaemonBootEpoch == "" || o.NativeEpoch == "" {
		return fmt.Errorf("%w: native owner is incomplete", protocol.ErrInvalidVscreenContract)
	}
	return nil
}

// AppClaimKey is host-wide; PID reuse must never inherit a previous claim.
type AppClaimKey struct {
	UID                  uint32 `json:"uid"`
	PID                  int    `json:"pid"`
	ProcessStartIdentity string `json:"process_start_identity"`
}

// Validate requires both a login identity and a process incarnation.
func (k AppClaimKey) Validate() error {
	if k.UID == 0 || k.PID <= 0 || k.ProcessStartIdentity == "" {
		return fmt.Errorf("%w: app claim identity is incomplete", protocol.ErrInvalidVscreenContract)
	}
	return nil
}

// AppClaim binds an entire process to one runtime, regardless of its window count.
type AppClaim struct {
	Key      AppClaimKey `json:"key"`
	Resource ResourceKey `json:"resource"`
	Owner    NativeOwner `json:"owner"`
}

// Validate prevents a process claim crossing the resource login boundary.
func (c AppClaim) Validate() error {
	if err := c.Key.Validate(); err != nil {
		return err
	}
	if err := c.Resource.Validate(); err != nil {
		return err
	}
	if err := c.Owner.Validate(); err != nil {
		return err
	}
	if c.Key.UID != c.Resource.UID {
		return fmt.Errorf("%w: app claim login mismatch", protocol.ErrInvalidVscreenContract)
	}
	return nil
}
