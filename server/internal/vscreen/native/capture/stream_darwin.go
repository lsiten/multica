//go:build darwin && cgo

package capture

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework ScreenCaptureKit -framework VideoToolbox -framework CoreMedia -framework CoreVideo
#include "bridge.h"
*/
import "C"
import (
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// Stream owns its native session. Close must confirm callback quiescence before release.
type Stream struct {
	mu     sync.Mutex
	handle C.uintptr_t
}

func nativeConfig(config Config) C.VSCaptureConfig {
	result := C.VSCaptureConfig{display_id: C.uint32_t(config.DisplayID), width: C.uint32_t(config.Width), height: C.uint32_t(config.Height), fps: C.uint32_t(config.FPS), bitrate: C.uint32_t(config.Bitrate), max_level: C.uint32_t(config.MaxLevelIDC)}
	if config.ShowCursor {
		result.cursor = 1
	}
	return result
}
func open(ctx context.Context, config Config) (*Stream, error) {
	native := nativeConfig(config)
	if len(config.ExcludedWindowIDs) > 0 {
		native.excluded = (*C.uint32_t)(unsafe.Pointer(&config.ExcludedWindowIDs[0]))
		native.excluded_count = C.uint32_t(len(config.ExcludedWindowIDs))
	}
	var handle C.uintptr_t
	if err := nativeError(C.vs_capture_open(native, &handle)); err != nil {
		return nil, err
	}
	stream := &Stream{handle: handle}
	if err := ctx.Err(); err != nil {
		cleanupErr := stream.Close(context.WithoutCancel(ctx))
		if cleanupErr != nil {
			return stream, fmt.Errorf("%w: %v", err, cleanupErr)
		}
		return nil, err
	}
	return stream, nil
}

// Next returns one complete access unit, waiting in bounded native intervals.
func (s *Stream) Next(ctx context.Context) (Sample, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Sample{}, err
		}
		s.mu.Lock()
		if s.handle == 0 {
			s.mu.Unlock()
			return Sample{}, ErrClosed
		}
		var sample C.VSEncodedSample
		status := C.vs_capture_next(s.handle, 50, &sample)
		if status != 0 {
			s.mu.Unlock()
			if status == 9 {
				continue
			}
			return Sample{}, nativeError(status)
		}
		result := Sample{AnnexB: C.GoBytes(unsafe.Pointer(sample.bytes), C.int(sample.length)), PTSNanos: int64(sample.pts_ns), DurationNanos: int64(sample.duration_ns), KeyFrame: sample.keyframe != 0}
		C.vs_sample_free(&sample)
		s.mu.Unlock()
		return result, nil
	}
}

// ForceKeyframe requests an IDR for the next available input frame.
func (s *Stream) ForceKeyframe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == 0 {
		return ErrClosed
	}
	return nativeError(C.vs_capture_force_keyframe(s.handle))
}

// Close freezes callbacks, drains input/encoder work and only then releases the native handle.
// A timeout leaves ownership intact and callers must keep the source unavailable until retry succeeds.
func (s *Stream) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == 0 {
		return nil
	}
	timeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	if timeout <= 0 {
		return ErrTimeout
	}
	if err := nativeError(C.vs_capture_close(s.handle, C.uint32_t(timeout.Milliseconds()))); err != nil {
		return err
	}
	s.handle = 0
	return nil
}

func nativeError(status C.int) error {
	switch status {
	case 0:
		return nil
	case 1:
		return ErrUnsupported
	case 2:
		return ErrPermission
	case 3:
		return ErrDisplay
	case 4:
		return ErrEncoder
	case 5:
		return ErrTimeout
	case 6:
		return ErrClosed
	case 7:
		return ErrStream
	case 8:
		return ErrConfig
	default:
		return fmt.Errorf("%w: native code %d", ErrStream, status)
	}
}

func newEncoderFixture(config Config) (*Stream, error) {
	var handle C.uintptr_t
	if err := nativeError(C.vs_encoder_fixture(nativeConfig(config), &handle)); err != nil {
		return nil, err
	}
	return &Stream{handle: handle}, nil
}
func (s *Stream) fixtureFrame(index uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == 0 {
		return ErrClosed
	}
	return nativeError(C.vs_encoder_fixture_frame(s.handle, C.uint32_t(index)))
}

// Stats returns native counters without exposing callback-owned buffers.
func (s *Stream) Stats() (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == 0 {
		return Stats{}, ErrClosed
	}
	var stats C.VSStreamStats
	if err := nativeError(C.vs_capture_stats(s.handle, &stats)); err != nil {
		return Stats{}, err
	}
	return Stats{uint32(stats.retained_input), uint32(stats.in_flight), uint32(stats.queued_samples), uint32(stats.peak_in_flight), uint32(stats.peak_queued), uint64(stats.dropped_input), uint64(stats.dropped_output), stats.hardware != 0, stats.hardware_known != 0, C.GoString(&stats.profile_level[0])}, nil
}

// PermissionGranted checks screen-recording consent without prompting or starting capture.
func PermissionGranted() (bool, error) {
	status := C.vs_capture_permission()
	if status == 2 {
		return false, nil
	}
	return status == 0, nativeError(status)
}
