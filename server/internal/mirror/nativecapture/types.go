// Package nativecapture owns the private, read-only display capture helper.
package nativecapture

import (
	"errors"

	"github.com/multica-ai/multica/server/internal/mirror/nativecapture/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const ProtocolVersion = 1

var (
	ErrProtocol    = errors.New("invalid native capture protocol")
	ErrUnsupported = errors.New("native capture unsupported")
	ErrUnavailable = errors.New("native display unavailable")
)

func Supported() bool { return supported() }

// Epoch invalidates queued frames when the helper, display, or geometry changes.
type Epoch struct {
	NativeEpoch       string `json:"native_epoch"`
	DisplayGeneration string `json:"display_generation"`
	GeometryRevision  uint64 `json:"geometry_revision"`
}

func (e Epoch) Validate() error {
	if e.NativeEpoch == "" || e.DisplayGeneration == "" || e.GeometryRevision == 0 {
		return ErrProtocol
	}
	return nil
}

// SourceDescriptor is a physical or system display that may be captured.
type SourceDescriptor struct {
	protocol.MirrorSourceBinding
	DisplayID        uint32  `json:"display_id"`
	Name             string  `json:"name"`
	Width            uint32  `json:"width"`
	Height           uint32  `json:"height"`
	LogicalWidth     float64 `json:"logical_width"`
	LogicalHeight    float64 `json:"logical_height"`
	Scale            float64 `json:"scale"`
	X                int32   `json:"x"`
	Y                int32   `json:"y"`
	GeometryRevision uint64  `json:"geometry_revision"`
}

// CaptureOptions selects one catalog source and never an arbitrary display ID.
type CaptureOptions struct {
	StreamID          string                `json:"stream_id"`
	Source            protocol.MirrorSource `json:"source"`
	Width             uint32                `json:"width,omitempty"`
	Height            uint32                `json:"height,omitempty"`
	FPS               uint32                `json:"fps,omitempty"`
	Bitrate           uint32                `json:"bitrate,omitempty"`
	ShowCursor        bool                  `json:"show_cursor"`
	MaxLevelIDC       uint32                `json:"max_level_idc,omitempty"`
	ExcludedWindowIDs []uint32              `json:"excluded_window_ids,omitempty"`
}

// CaptureDescriptor binds encoder output to the immutable source geometry.
type CaptureDescriptor struct {
	StreamID    string           `json:"stream_id"`
	Source      SourceDescriptor `json:"source"`
	Layout      capture.Layout   `json:"layout"`
	FPS         uint32           `json:"fps"`
	Bitrate     uint32           `json:"bitrate"`
	MaxLevelIDC uint32           `json:"max_level_idc,omitempty"`
	Encoder     *capture.Stats   `json:"encoder,omitempty"`
}
