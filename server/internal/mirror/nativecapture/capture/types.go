// Package capture owns continuous SCStream capture and VideoToolbox sample lifetime.
package capture

import (
	"context"
	"errors"
)

const MaxSampleBytes = 8 * 1024 * 1024

var (
	ErrConfig      = errors.New("invalid capture configuration")
	ErrUnsupported = errors.New("native capture unsupported")
	ErrPermission  = errors.New("screen recording permission denied")
	ErrDisplay     = errors.New("capture display unavailable")
	ErrEncoder     = errors.New("native h264 encoder unavailable")
	ErrTimeout     = errors.New("native capture shutdown or startup uncertain")
	ErrClosed      = errors.New("native capture closed")
	ErrStream      = errors.New("native capture stream failed")
)

// Config selects one exact system display. Its caller must authorize that source.
// Width and Height scale capture output; they never change display configuration.
type Config struct {
	DisplayID         uint32   `json:"display_id"`
	Width             uint32   `json:"width"`
	Height            uint32   `json:"height"`
	FPS               uint32   `json:"fps"`
	Bitrate           uint32   `json:"bitrate"`
	ExcludedWindowIDs []uint32 `json:"excluded_window_ids,omitempty"`
	ShowCursor        bool     `json:"show_cursor"`
	MaxLevelIDC       uint32   `json:"max_level_idc,omitempty"`
}

func (c Config) validate() error {
	if c.DisplayID == 0 || c.Width < 16 || c.Width > 4096 || c.Height < 16 || c.Height > 2160 || c.Width%2 != 0 || c.Height%2 != 0 || c.FPS < 1 || c.FPS > 60 || c.Bitrate < 100000 || c.Bitrate > 50000000 || len(c.ExcludedWindowIDs) > 128 {
		return ErrConfig
	}
	if c.MaxLevelIDC != 0 {
		limits, ok := map[uint32][2]uint64{31: {3600, 108000}, 40: {8192, 245760}}[c.MaxLevelIDC]
		blocks := uint64((c.Width+15)/16) * uint64((c.Height+15)/16)
		if !ok || blocks > limits[0] || blocks*uint64(c.FPS) > limits[1] {
			return ErrConfig
		}
	}
	for _, id := range c.ExcludedWindowIDs {
		if id == 0 {
			return ErrConfig
		}
	}
	return nil
}

// Sample contains a complete owned Annex-B access unit, with SPS/PPS before IDR.
// No native callback-owned memory survives into this value.
type Sample struct {
	AnnexB        []byte
	PTSNanos      int64
	DurationNanos int64
	KeyFrame      bool
}

// Open starts a continuous exact-display stream after nonprompting TCC preflight.
// If cancellation cleanup is uncertain, it returns a nonnil Stream with the error;
// the caller must retain that stream and retry Close before releasing ownership.
func Open(ctx context.Context, config Config) (*Stream, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return open(ctx, config)
}

// Stats reports actual bounded-buffer occupancy and the selected VideoToolbox implementation.
type Stats struct {
	RetainedInput        uint32 `json:"retained_input"`
	InFlight             uint32 `json:"in_flight"`
	QueuedSamples        uint32 `json:"queued_samples"`
	PeakInFlight         uint32 `json:"peak_in_flight"`
	PeakQueued           uint32 `json:"peak_queued"`
	DroppedInput         uint64 `json:"dropped_input"`
	DroppedOutput        uint64 `json:"dropped_output"`
	HardwareAccelerated  bool   `json:"hardware_accelerated"`
	HardwareKnown        bool   `json:"hardware_known"`
	ActualProfileLevelID string `json:"actual_profile_level_id,omitempty"`
}
