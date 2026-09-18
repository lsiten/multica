package daemon

import (
	"errors"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
)

// VscreenPerformanceRequest is the benchmark target, separate from negotiated video geometry.
type VscreenPerformanceRequest struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	FPS    int `json:"fps"`
}

// VscreenPerformanceSmokeConfig is read only from the nonce-private coordinator configuration.
type VscreenPerformanceSmokeConfig struct {
	SchemaVersion int                       `json:"schema_version"`
	Nonce         string                    `json:"nonce"`
	ParentPID     int                       `json:"parent_pid"`
	EvidenceDir   string                    `json:"evidence_dir"`
	Mode          string                    `json:"mode"`
	DurationMS    int64                     `json:"duration_ms"`
	Cycles        int                       `json:"cycles"`
	Requested     VscreenPerformanceRequest `json:"requested"`
}
type performanceMarker struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	CellSize  float64 `json:"cell_size"`
	Columns   int     `json:"columns"`
	Available bool    `json:"available"`
	Reason    string  `json:"reason,omitempty"`
}
type performanceSource struct {
	SourceID    string            `json:"source_id"`
	RuntimeID   string            `json:"runtime_id"`
	SourceTag   uint32            `json:"source_tag"`
	NativeEpoch string            `json:"native_epoch"`
	Generation  string            `json:"generation"`
	Marker      performanceMarker `json:"marker"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	FPS         int               `json:"fps"`
	source      native.SourceDescriptor
}
type performanceReady struct {
	Mode          string              `json:"mode"`
	DurationMS    int64               `json:"duration_ms"`
	Cycles        int                 `json:"cycles"`
	Type          string              `json:"type"`
	SchemaVersion int                 `json:"schema_version"`
	Executable    string              `json:"executable"`
	Version       string              `json:"version"`
	Commit        string              `json:"commit"`
	BaseURL       string              `json:"base_url"`
	HostClock     string              `json:"host_clock"`
	Sources       []performanceSource `json:"sources"`
	Nonce         string              `json:"nonce,omitempty"`
}
type performanceAvailability struct {
	Method    string `json:"method,omitempty"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type performanceResourceSample struct {
	ActiveCallbacks *uint32                     `json:"active_callbacks,omitempty"`
	ActiveEncoders  *uint32                     `json:"active_encoders,omitempty"`
	ElapsedMS       float64                     `json:"elapsed_ms"`
	RSS             *uint64                     `json:"rss_bytes,omitempty"`
	CPU             string                      `json:"cpu_time_ns,omitempty"`
	FD              *int                        `json:"fd_count,omitempty"`
	Bytes           *uint64                     `json:"bytes_sent_total,omitempty"`
	EncoderStats    []*native.CaptureDescriptor `json:"encoder_stats,omitempty"`
}
type performanceResources struct {
	Availability map[string]performanceAvailability `json:"availability"`
	Samples      []performanceResourceSample        `json:"samples"`
}
type performanceShared struct {
	CaptureSessions       int    `json:"capture_sessions"`
	EncoderSessions       *int   `json:"encoder_sessions"`
	IndependentlyObserved bool   `json:"independently_observed"`
	TotalCaptureOpens     uint64 `json:"total_capture_opens"`
	Reason                string `json:"reason,omitempty"`
}
type performanceCycle struct {
	CaptureOpened         bool `json:"capture_opened"`
	EncodedFrameReceived  bool `json:"encoded_frame_received"`
	Cycle                 int  `json:"cycle"`
	Disposed              bool `json:"disposed"`
	FixtureExited         bool `json:"fixture_exited"`
	ManagedDisplaysAfter  *int `json:"managed_displays_after,omitempty"`
	ActiveCallbacksAfter  *int `json:"active_callbacks_after,omitempty"`
	ActiveEncodersAfter   *int `json:"active_encoders_after,omitempty"`
	FDDelta               *int `json:"fd_delta,omitempty"`
	MeasurementsAvailable bool `json:"measurements_available"`
}
type performanceResult struct {
	SystemGPU        performanceSystemGPU            `json:"system_gpu"`
	Type             string                          `json:"type"`
	SchemaVersion    int                             `json:"schema_version"`
	Resources        map[string]performanceResources `json:"resources"`
	Shared           performanceShared               `json:"shared_source"`
	Cycles           []performanceCycle              `json:"cycles"`
	CleanupConfirmed bool                            `json:"cleanup_confirmed"`
	Errors           []string                        `json:"errors"`
}
type performanceOfferRequest struct {
	ViewerID string                    `json:"viewer_id"`
	SourceID string                    `json:"source_id"`
	Offer    mirror.SessionDescription `json:"offer"`
}
type performanceOfferResponse struct {
	Answer     mirror.SessionDescription `json:"answer"`
	SourceID   string                    `json:"source_id"`
	SourceTag  uint32                    `json:"source_tag"`
	Marker     performanceMarker         `json:"marker"`
	Negotiated VscreenPerformanceRequest `json:"negotiated"`
}
type performanceProcessReading struct {
	Start, RSS, CPU uint64
	FD              int
}

var errPerformanceMetricUnavailable = errors.New("owned_process_metric_unavailable")
