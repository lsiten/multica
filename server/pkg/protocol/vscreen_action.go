package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

// VscreenActionTarget is resolved from task authority and a current native snapshot.
// A window handle is opaque; clients cannot substitute a PID or physical display.
type VscreenActionTarget struct {
	Resource         ResourceKey  `json:"resource"`
	TaskID           string       `json:"task_id"`
	TransactionID    string       `json:"transaction_id"`
	LeaseEpoch       uint64       `json:"lease_epoch"`
	Epoch            VscreenEpoch `json:"epoch"`
	WindowHandle     string       `json:"window_handle"`
	SnapshotRevision uint64       `json:"snapshot_revision"`
}

// Validate requires every part of the action's observation and lease address.
func (t VscreenActionTarget) Validate() error {
	if err := t.Resource.Validate(); err != nil {
		return err
	}
	if err := t.Epoch.Validate(); err != nil {
		return err
	}
	if !vscreenIdentity(t.TaskID) || !vscreenIdentity(t.TransactionID) || t.LeaseEpoch == 0 || !vscreenIdentity(t.WindowHandle) || t.SnapshotRevision == 0 {
		return fmt.Errorf("%w: incomplete action target", ErrInvalidVscreenContract)
	}
	return nil
}

// VscreenActionKind enumerates supported task input operations.
type VscreenActionKind string

const (
	VscreenActionClick  VscreenActionKind = "click"
	VscreenActionType   VscreenActionKind = "type"
	VscreenActionKey    VscreenActionKind = "key"
	VscreenActionScroll VscreenActionKind = "scroll"
	VscreenActionDrag   VscreenActionKind = "drag"
)

// VscreenPoint uses observed frame pixels; native code owns the display transform.
type VscreenPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// VscreenClickAction selects exactly one semantic element or frame position.
type VscreenClickAction struct {
	ElementHandle string        `json:"element_handle,omitempty"`
	Position      *VscreenPoint `json:"position,omitempty"`
}

// VscreenTypeAction requests semantic Unicode value input, without clipboard mutation.
type VscreenTypeAction struct {
	ElementHandle string `json:"element_handle"`
	Text          string `json:"text"`
}

// VscreenKeyAction requests a named key; native capability probing still gates dispatch.
type VscreenKeyAction struct {
	Key       string   `json:"key"`
	Modifiers []string `json:"modifiers,omitempty"`
}

// VscreenScrollAction requests bounded deltas relative to a current snapshot.
type VscreenScrollAction struct {
	Position VscreenPoint `json:"position"`
	DeltaX   float64      `json:"delta_x"`
	DeltaY   float64      `json:"delta_y"`
}

// VscreenDragAction describes one bounded native operation between observed frame pixels.
type VscreenDragAction struct {
	From       VscreenPoint `json:"from"`
	To         VscreenPoint `json:"to"`
	DurationMS uint32       `json:"duration_ms"`
}

// VscreenAction is a checked tagged union. Exactly the matching payload is allowed.
type VscreenAction struct {
	Kind   VscreenActionKind    `json:"kind"`
	Click  *VscreenClickAction  `json:"click,omitempty"`
	Type   *VscreenTypeAction   `json:"type,omitempty"`
	Key    *VscreenKeyAction    `json:"key,omitempty"`
	Scroll *VscreenScrollAction `json:"scroll,omitempty"`
	Drag   *VscreenDragAction   `json:"drag,omitempty"`
}

// Validate rejects unknown actions and ambiguous union payloads.
func (a VscreenAction) Validate() error {
	count := 0
	if a.Click != nil {
		count++
	}
	if a.Type != nil {
		count++
	}
	if a.Key != nil {
		count++
	}
	if a.Scroll != nil {
		count++
	}
	if a.Drag != nil {
		count++
	}
	if count != 1 {
		return fmt.Errorf("%w: action must have one payload", ErrInvalidVscreenContract)
	}
	valid := false
	switch a.Kind {
	case VscreenActionClick:
		if a.Click != nil {
			valid = a.Click.Position == nil && vscreenIdentity(a.Click.ElementHandle) || a.Click.ElementHandle == "" && a.Click.Position != nil && validVscreenPoint(*a.Click.Position)
		}
	case VscreenActionType:
		valid = a.Type != nil && vscreenIdentity(a.Type.ElementHandle) && utf8.ValidString(a.Type.Text) && len(a.Type.Text) <= 8192
	case VscreenActionKey:
		if a.Key != nil && vscreenIdentity(a.Key.Key) && len(a.Key.Modifiers) <= 4 {
			valid = true
			seen := make(map[string]bool, len(a.Key.Modifiers))
			for _, modifier := range a.Key.Modifiers {
				switch modifier {
				case "shift", "control", "alt", "meta":
				default:
					valid = false
				}
				if seen[modifier] {
					valid = false
				}
				seen[modifier] = true
			}
		}
	case VscreenActionScroll:
		valid = a.Scroll != nil && validVscreenPoint(a.Scroll.Position) && finiteVscreenNumber(a.Scroll.DeltaX) && finiteVscreenNumber(a.Scroll.DeltaY) && math.Abs(a.Scroll.DeltaX) <= 10000 && math.Abs(a.Scroll.DeltaY) <= 10000
	case VscreenActionDrag:
		valid = a.Drag != nil && validVscreenPoint(a.Drag.From) && validVscreenPoint(a.Drag.To) && a.Drag.DurationMS > 0 && a.Drag.DurationMS <= 3000
	default:
		return fmt.Errorf("%w: unknown action kind", ErrInvalidVscreenContract)
	}
	if !valid {
		return fmt.Errorf("%w: invalid action payload", ErrInvalidVscreenContract)
	}
	return nil
}

func finiteVscreenNumber(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func validVscreenPoint(p VscreenPoint) bool {
	return finiteVscreenNumber(p.X) && finiteVscreenNumber(p.Y) && p.X >= 0 && p.Y >= 0
}

// VscreenActionRequest carries idempotency identity; duplicate dispatch is the actor's responsibility.
type VscreenActionRequest struct {
	Target   VscreenActionTarget `json:"target"`
	ActionID string              `json:"action_id"`
	Sequence uint64              `json:"sequence"`
	Action   VscreenAction       `json:"action"`
}

// Validate checks wire shape independently of live lease authorization.
func (r VscreenActionRequest) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return err
	}
	if !vscreenIdentity(r.ActionID) || r.Sequence == 0 {
		return fmt.Errorf("%w: action identity is incomplete", ErrInvalidVscreenContract)
	}
	return r.Action.Validate()
}

// ParseVscreenAction rejects arbitrary targets and compares all live lease/snapshot generations.
// The actor must check lease expiry, cancellation, sequence, and native ownership before dispatch.
func ParseVscreenAction(raw []byte, current VscreenActionTarget) (VscreenActionRequest, error) {
	var request VscreenActionRequest
	if err := decodeVscreenJSON(raw, &request); err != nil {
		return request, err
	}
	if err := request.Validate(); err != nil {
		return VscreenActionRequest{}, err
	}
	if request.Target != current {
		return VscreenActionRequest{}, fmt.Errorf("%w: stale or unauthorized action target", ErrInvalidVscreenContract)
	}
	return request, nil
}

func decodeVscreenJSON[T any](raw []byte, target *T) error {
	if len(raw) > 64*1024 || !utf8.Valid(raw) {
		return fmt.Errorf("%w: invalid control payload encoding or size", ErrInvalidVscreenContract)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode virtual screen control: %w", err)
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return fmt.Errorf("%w: trailing control payload", ErrInvalidVscreenContract)
	}
	return nil
}
