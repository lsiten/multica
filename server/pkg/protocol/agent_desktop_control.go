package protocol

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	EventAgentDesktopControlStartQuery  = "agent-desktop:control-start-query"
	EventAgentDesktopControlStartResult = "agent-desktop:control-start-result"
	EventAgentDesktopControlInputQuery  = "agent-desktop:control-input-query"
	EventAgentDesktopControlInputResult = "agent-desktop:control-input-result"
	EventAgentDesktopControlStopQuery   = "agent-desktop:control-stop-query"
	EventAgentDesktopControlStopResult  = "agent-desktop:control-stop-result"
)

type AgentDesktopControlAction string

const (
	AgentDesktopControlMove         AgentDesktopControlAction = "move"
	AgentDesktopControlMoveRelative AgentDesktopControlAction = "move_relative"
	AgentDesktopControlClick        AgentDesktopControlAction = "click"
	AgentDesktopControlDoubleClick  AgentDesktopControlAction = "double_click"
	AgentDesktopControlRightClick   AgentDesktopControlAction = "right_click"
	AgentDesktopControlMouseDown    AgentDesktopControlAction = "mouse_down"
	AgentDesktopControlMouseUp      AgentDesktopControlAction = "mouse_up"
	AgentDesktopControlDrag         AgentDesktopControlAction = "drag"
	AgentDesktopControlScroll       AgentDesktopControlAction = "scroll"
	AgentDesktopControlPress        AgentDesktopControlAction = "press"
	AgentDesktopControlText         AgentDesktopControlAction = "text"
	AgentDesktopControlClipboardGet AgentDesktopControlAction = "clipboard_get"
	AgentDesktopControlClipboardSet AgentDesktopControlAction = "clipboard_set"
)

type AgentDesktopControlStartQuery struct {
	RuntimeEnvelope
	ControlID string `json:"control_id"`
	Name      string `json:"name"`
}

func (q AgentDesktopControlStartQuery) Validate() error {
	if err := q.RuntimeEnvelope.Validate(); err != nil {
		return err
	}
	if !mirrorIdentity(q.ControlID) || !shortProtocolText(q.Name, 80) {
		return fmt.Errorf("%w: invalid desktop control start", ErrInvalidMirrorContract)
	}
	return nil
}

type AgentDesktopControlInput struct {
	Source MirrorSource              `json:"source"`
	Action AgentDesktopControlAction `json:"action"`
	X      *float64                  `json:"x,omitempty"`
	Y      *float64                  `json:"y,omitempty"`
	X2     *float64                  `json:"x2,omitempty"`
	Y2     *float64                  `json:"y2,omitempty"`
	DX     int                       `json:"dx,omitempty"`
	DY     int                       `json:"dy,omitempty"`
	Button string                    `json:"button,omitempty"`
	Key    string                    `json:"key,omitempty"`
	Text   string                    `json:"text,omitempty"`
}

func (c AgentDesktopControlInput) Validate() error {
	if len(c.Text) > 65536 || !utf8.ValidString(c.Text) || strings.ContainsRune(c.Text, 0) ||
		len(c.Key) > 80 || strings.ContainsRune(c.Key, 0) || (c.Button != "" && c.Button != "left" && c.Button != "right" && c.Button != "middle") ||
		c.DX < -200 || c.DX > 200 || c.DY < -200 || c.DY > 200 {
		return fmt.Errorf("%w: invalid desktop control input", ErrInvalidMirrorContract)
	}
	if err := c.Source.Validate(); err != nil {
		return err
	}
	switch c.Action {
	case AgentDesktopControlMove, AgentDesktopControlClick, AgentDesktopControlDoubleClick,
		AgentDesktopControlRightClick, AgentDesktopControlScroll, AgentDesktopControlMouseDown,
		AgentDesktopControlMouseUp:
		if !validUnitCoordinate(c.X) || !validUnitCoordinate(c.Y) {
			return fmt.Errorf("%w: pointer coordinates are required", ErrInvalidMirrorContract)
		}
	case AgentDesktopControlMoveRelative:
		if c.X == nil || c.Y == nil || *c.X < -1 || *c.X > 1 || *c.Y < -1 || *c.Y > 1 {
			return fmt.Errorf("%w: relative pointer delta is required", ErrInvalidMirrorContract)
		}
	case AgentDesktopControlDrag:
		if !validUnitCoordinate(c.X) || !validUnitCoordinate(c.Y) ||
			!validUnitCoordinate(c.X2) || !validUnitCoordinate(c.Y2) {
			return fmt.Errorf("%w: drag coordinates are required", ErrInvalidMirrorContract)
		}
	case AgentDesktopControlPress:
		if strings.TrimSpace(c.Key) == "" {
			return fmt.Errorf("%w: key combo is required", ErrInvalidMirrorContract)
		}
	case AgentDesktopControlText, AgentDesktopControlClipboardSet:
		if c.Text == "" {
			return fmt.Errorf("%w: text is required", ErrInvalidMirrorContract)
		}
	case AgentDesktopControlClipboardGet:
	default:
		return fmt.Errorf("%w: unknown desktop control action", ErrInvalidMirrorContract)
	}
	return nil
}

func validUnitCoordinate(value *float64) bool {
	return value != nil && *value >= 0 && *value <= 1
}

type AgentDesktopControlInputQuery struct {
	RuntimeEnvelope
	ControlID string                   `json:"control_id"`
	Input     AgentDesktopControlInput `json:"input"`
}

func (q AgentDesktopControlInputQuery) Validate() error {
	if err := q.RuntimeEnvelope.Validate(); err != nil {
		return err
	}
	if !mirrorIdentity(q.ControlID) {
		return fmt.Errorf("%w: desktop control identity is required", ErrInvalidMirrorContract)
	}
	return q.Input.Validate()
}

type AgentDesktopControlStopQuery struct {
	RuntimeEnvelope
	ControlID string `json:"control_id"`
}

func (q AgentDesktopControlStopQuery) Validate() error {
	if err := q.RuntimeEnvelope.Validate(); err != nil {
		return err
	}
	if !mirrorIdentity(q.ControlID) {
		return fmt.Errorf("%w: desktop control identity is required", ErrInvalidMirrorContract)
	}
	return nil
}

type AgentDesktopControlResult struct {
	RuntimeEnvelope
	ControlID string  `json:"control_id"`
	State     string  `json:"state"`
	Reason    string  `json:"reason,omitempty"`
	Clipboard *string `json:"clipboard,omitempty"`
}

func (r AgentDesktopControlResult) Validate() error {
	if err := r.RuntimeEnvelope.Validate(); err != nil {
		return err
	}
	if !mirrorIdentity(r.ControlID) || (r.State != "started" && r.State != "ok" && r.State != "ended" && r.State != "failed") ||
		len(r.Reason) > 120 || !utf8.ValidString(r.Reason) ||
		(r.Clipboard != nil && (len(*r.Clipboard) > 65536 || !utf8.ValidString(*r.Clipboard))) {
		return fmt.Errorf("%w: invalid desktop control result", ErrInvalidMirrorContract)
	}
	return nil
}
