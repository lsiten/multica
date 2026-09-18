package capture

// LiveResourceStats are independent counters in the actual native process, including
// resources retained by failed or unconfirmed shutdown. Unavailable is not zero.
type LiveResourceStats struct {
	Available       bool   `json:"available"`
	Reason          string `json:"reason,omitempty"`
	CaptureSessions uint32 `json:"capture_sessions"`
	EncoderSessions uint32 `json:"encoder_sessions"`
	ActiveCallbacks uint32 `json:"active_callbacks"`
}

// LiveResources reads only counters; it never starts capture or requests permission.
func LiveResources() LiveResourceStats { return liveResources() }
