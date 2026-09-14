package protocol

import "fmt"

// UnmarshalJSON preserves the difference between absent coordinates and the valid origin.
func (p *VscreenPoint) UnmarshalJSON(raw []byte) error {
	var value struct {
		X *float64 `json:"x"`
		Y *float64 `json:"y"`
	}
	if err := decodeVscreenJSON(raw, &value); err != nil {
		return err
	}
	if value.X == nil || value.Y == nil {
		return fmt.Errorf("%w: missing frame coordinates", ErrInvalidVscreenContract)
	}
	*p = VscreenPoint{X: *value.X, Y: *value.Y}
	return nil
}

// UnmarshalJSON permits explicit empty text but never clears a value due to a missing field.
func (a *VscreenTypeAction) UnmarshalJSON(raw []byte) error {
	var value struct {
		ElementHandle string  `json:"element_handle"`
		Text          *string `json:"text"`
	}
	if err := decodeVscreenJSON(raw, &value); err != nil {
		return err
	}
	if value.Text == nil {
		return fmt.Errorf("%w: missing input text", ErrInvalidVscreenContract)
	}
	*a = VscreenTypeAction{ElementHandle: value.ElementHandle, Text: *value.Text}
	return nil
}

// UnmarshalJSON requires the full observed scroll address and both explicit deltas.
func (a *VscreenScrollAction) UnmarshalJSON(raw []byte) error {
	var value struct {
		Position *VscreenPoint `json:"position"`
		DeltaX   *float64      `json:"delta_x"`
		DeltaY   *float64      `json:"delta_y"`
	}
	if err := decodeVscreenJSON(raw, &value); err != nil {
		return err
	}
	if value.Position == nil || value.DeltaX == nil || value.DeltaY == nil {
		return fmt.Errorf("%w: missing scroll address or delta", ErrInvalidVscreenContract)
	}
	*a = VscreenScrollAction{Position: *value.Position, DeltaX: *value.DeltaX, DeltaY: *value.DeltaY}
	return nil
}

// UnmarshalJSON requires both observed drag endpoints before native dispatch.
func (a *VscreenDragAction) UnmarshalJSON(raw []byte) error {
	var value struct {
		From       *VscreenPoint `json:"from"`
		To         *VscreenPoint `json:"to"`
		DurationMS uint32        `json:"duration_ms"`
	}
	if err := decodeVscreenJSON(raw, &value); err != nil {
		return err
	}
	if value.From == nil || value.To == nil {
		return fmt.Errorf("%w: missing drag endpoints", ErrInvalidVscreenContract)
	}
	*a = VscreenDragAction{From: *value.From, To: *value.To, DurationMS: value.DurationMS}
	return nil
}
