package native

import (
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// CaptureOptions selects an authorized catalog source, never an arbitrary display ID.
type CaptureOptions struct {
	StreamID          string                `json:"stream_id"`
	Source            protocol.MirrorSource `json:"source"`
	Width             uint32                `json:"width,omitempty"`
	Height            uint32                `json:"height,omitempty"`
	FPS               uint32                `json:"fps,omitempty"`
	Bitrate           uint32                `json:"bitrate,omitempty"`
	ExcludedWindowIDs []uint32              `json:"excluded_window_ids,omitempty"`
	ShowCursor        bool                  `json:"show_cursor"`
	MaxLevelIDC       uint32                `json:"max_level_idc,omitempty"`
}

// CaptureDescriptor binds the encoder output geometry to its original display geometry.
type CaptureDescriptor struct {
	StreamID    string           `json:"stream_id"`
	Source      SourceDescriptor `json:"source"`
	Layout      capture.Layout   `json:"layout"`
	FPS         uint32           `json:"fps"`
	Bitrate     uint32           `json:"bitrate"`
	MaxLevelIDC uint32           `json:"max_level_idc,omitempty"`
	Encoder     *capture.Stats   `json:"encoder,omitempty"`
}
