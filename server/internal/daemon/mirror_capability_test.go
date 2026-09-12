package daemon

import (
	"slices"
	"testing"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestDaemonCommonCapabilitiesMirrorMatchesCompiledCapturer(t *testing.T) {
	capabilities := daemonCommonCapabilities()
	declared := slices.Contains(capabilities, protocol.DaemonCapabilityScreenMirrorV1)

	if declared != mirror.NativeCaptureSupported() {
		t.Fatalf("screen mirror capability = %v, want %v", declared, mirror.NativeCaptureSupported())
	}
}
