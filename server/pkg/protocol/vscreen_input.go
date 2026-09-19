package protocol

import (
	"encoding/json"
	"fmt"
)

// Mirror input travels over the P2P "mirror-input" data channel. The server
// only negotiates the channel; it never sees these payloads. Messages are
// intentionally compact JSON because input is low-rate relative to frames.

// MirrorInputKind enumerates the human input operations accepted on a mirror.
type MirrorInputKind string

const (
	MirrorInputPointerDown  MirrorInputKind = "pointer:down"
	MirrorInputPointerUp    MirrorInputKind = "pointer:up"
	MirrorInputPointerMove  MirrorInputKind = "pointer:move"
	MirrorInputWheel        MirrorInputKind = "wheel"
	MirrorInputKeyDown      MirrorInputKind = "key:down"
	MirrorInputKeyUp        MirrorInputKind = "key:up"
	MirrorInputType         MirrorInputKind = "type"
)

// MirrorPointerButton names the pointer buttons a human may press.
type MirrorPointerButton string

const (
	MirrorButtonLeft   MirrorPointerButton = "left"
	MirrorButtonMiddle MirrorPointerButton = "middle"
	MirrorButtonRight  MirrorPointerButton = "right"
)

// MirrorInputMessage is one human input event bound to the viewer's control
// grant and the observed frame geometry. Coordinates are frame pixels; native
// code owns the transform to the target display.
type MirrorInputMessage struct {
	Kind             MirrorInputKind     `json:"kind"`
	GrantID          string              `json:"grant_id"`
	GestureID        string              `json:"gesture_id"`
	Seq              uint64              `json:"seq"`
	NativeEpoch      string              `json:"native_epoch"`
	DisplayGeneration string              `json:"display_generation"`
	GeometryRevision uint64              `json:"geometry_revision"`
	Pointer          *MirrorPointerInput `json:"pointer,omitempty"`
	Key              *MirrorKeyInput     `json:"key,omitempty"`
	Text             *MirrorTextInput    `json:"text,omitempty"`
}

// MirrorPointerInput describes a pointer event in observed frame pixels.
type MirrorPointerInput struct {
	Button MirrorPointerButton `json:"button,omitempty"`
	X      float64             `json:"x"`
	Y      float64             `json:"y"`
	DeltaX float64             `json:"delta_x,omitempty"`
	DeltaY float64             `json:"delta_y,omitempty"`
}

// MirrorKeyInput requests one named key with optional modifiers.
type MirrorKeyInput struct {
	Key       string   `json:"key"`
	Modifiers []string `json:"modifiers,omitempty"`
}

// MirrorTextInput requests semantic Unicode text entry without clipboard use.
type MirrorTextInput struct {
	Text string `json:"text"`
}

// MaxMirrorInputBytes bounds a single input message.
const MaxMirrorInputBytes = 16 * 1024

// Validate enforces the tagged union and the bounded, named-only key contract.
func (m MirrorInputMessage) Validate() error {
	if !vscreenIdentity(m.GrantID) || !vscreenIdentity(m.GestureID) || !vscreenIdentity(m.NativeEpoch) ||
		!vscreenIdentity(m.DisplayGeneration) || m.GeometryRevision == 0 {
		return fmt.Errorf("%w: input binding is incomplete", ErrInvalidMirrorDescription)
	}
	switch m.Kind {
	case MirrorInputPointerDown, MirrorInputPointerUp:
		if m.Pointer == nil || m.Key != nil || m.Text != nil {
			return fmt.Errorf("%w: pointer event requires pointer payload", ErrInvalidMirrorDescription)
		}
		if m.Pointer.Button != MirrorButtonLeft && m.Pointer.Button != MirrorButtonMiddle && m.Pointer.Button != MirrorButtonRight {
			return fmt.Errorf("%w: pointer button is required", ErrInvalidMirrorDescription)
		}
		if err := m.Pointer.validatePoint(); err != nil {
			return err
		}
	case MirrorInputPointerMove:
		if m.Pointer == nil || m.Key != nil || m.Text != nil {
			return fmt.Errorf("%w: move event requires pointer payload", ErrInvalidMirrorDescription)
		}
		if err := m.Pointer.validatePoint(); err != nil {
			return err
		}
	case MirrorInputWheel:
		if m.Pointer == nil || m.Key != nil || m.Text != nil {
			return fmt.Errorf("%w: wheel event requires pointer payload", ErrInvalidMirrorDescription)
		}
		if m.Pointer.Button != "" {
			return fmt.Errorf("%w: wheel must not carry a button", ErrInvalidMirrorDescription)
		}
		if err := m.Pointer.validatePoint(); err != nil {
			return err
		}
	case MirrorInputKeyDown, MirrorInputKeyUp:
		if m.Key == nil || m.Pointer != nil || m.Text != nil || !allowedMirrorKey(m.Key.Key) {
			return fmt.Errorf("%w: key event requires a named key", ErrInvalidMirrorDescription)
		}
		for _, mod := range m.Key.Modifiers {
			if !allowedMirrorModifier(mod) {
				return fmt.Errorf("%w: unknown key modifier", ErrInvalidMirrorDescription)
			}
		}
	case MirrorInputType:
		if m.Text == nil || m.Pointer != nil || m.Key != nil || m.Text.Text == "" {
			return fmt.Errorf("%w: type event requires non-empty text", ErrInvalidMirrorDescription)
		}
		if len([]rune(m.Text.Text)) > 1024 {
			return fmt.Errorf("%w: text too long", ErrInvalidMirrorDescription)
		}
	default:
		return fmt.Errorf("%w: unknown input kind", ErrInvalidMirrorDescription)
	}
	return nil
}

func (p MirrorPointerInput) validatePoint() error {
	if p.X < 0 || p.Y < 0 {
		return fmt.Errorf("%w: pointer coordinates must be non-negative", ErrInvalidMirrorDescription)
	}
	return nil
}

// ParseMirrorInputMessage decodes one bounded input message.
func ParseMirrorInputMessage(raw []byte) (MirrorInputMessage, error) {
	if len(raw) == 0 || len(raw) > MaxMirrorInputBytes {
		return MirrorInputMessage{}, fmt.Errorf("%w: input message size", ErrInvalidMirrorDescription)
	}
	var m MirrorInputMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return MirrorInputMessage{}, fmt.Errorf("%w: decode input: %v", ErrInvalidMirrorDescription, err)
	}
	if err := m.Validate(); err != nil {
		return MirrorInputMessage{}, err
	}
	return m, nil
}

// MirrorInputAck is returned on the same data channel. A nack names a fixed
// reason taxonomy so the UI can show contention/stale/denied without payloads.
type MirrorInputAck struct {
	Type      string `json:"type"`
	Kind      string `json:"kind"`
	GestureID string `json:"gesture_id"`
	Seq       uint64 `json:"seq"`
	Reason    string `json:"reason,omitempty"`
}

const (
	MirrorInputAckTypeOK   = "mirror-input:ack"
	MirrorInputAckTypeNack = "mirror-input:nack"
)

// Mirror input nack reasons.
const (
	MirrorInputBusy        = "busy"
	MirrorInputStale       = "stale"
	MirrorInputDenied      = "denied"
	MirrorInputUnsupported = "unsupported"
)

func allowedMirrorModifier(mod string) bool {
	switch mod {
	case "shift", "control", "alt", "meta":
		return true
	default:
		return false
	}
}

// allowedMirrorKey mirrors the certified native key set; no arbitrary key codes.
func allowedMirrorKey(key string) bool {
	if key == "" {
		return false
	}
	if len(key) == 1 {
		c := key[0]
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	switch key {
	case "enter", "return", "tab", "space", "escape", "backspace", "delete",
		"arrowleft", "arrowright", "arrowup", "arrowdown":
		return true
	default:
		return false
	}
}
