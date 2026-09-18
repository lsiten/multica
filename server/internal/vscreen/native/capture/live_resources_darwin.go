//go:build darwin && cgo

package capture

/*
#include "live_resources.h"
*/
import "C"

func liveResources() LiveResourceStats {
	s := C.vs_live_resources()
	if !bool(s.valid) {
		return LiveResourceStats{Reason: "native_counter_integrity_failed"}
	}
	return LiveResourceStats{Available: true, CaptureSessions: uint32(s.capture_sessions), EncoderSessions: uint32(s.encoder_sessions), ActiveCallbacks: uint32(s.active_callbacks)}
}
