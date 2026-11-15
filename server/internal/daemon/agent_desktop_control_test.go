package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/mirror/nativecapture"
)

func TestAgentDesktopPointUsesLogicalDisplayCoordinates(t *testing.T) {
	t.Parallel()
	source := nativecapture.SourceDescriptor{
		X:             1512,
		Y:             0,
		LogicalWidth:  1512,
		LogicalHeight: 982,
		Scale:         2,
	}
	x, y, err := agentDesktopPoint(source, ptr(0.5), ptr(0.5))
	if err != nil {
		t.Fatalf("agentDesktopPoint: %v", err)
	}
	if x != 2268 || y != 491 {
		t.Fatalf("point = %d,%d, want 2268,491", x, y)
	}
}

func TestAgentDesktopRelativePointTracksAndClamps(t *testing.T) {
	t.Parallel()
	control := &agentDesktopControlSession{}
	x, y := control.relativePoint(ptr(0.2), ptr(-0.1))
	if x != 0.7 || y != 0.4 {
		t.Fatalf("first relative point = %v,%v, want 0.7,0.4", x, y)
	}
	x, y = control.relativePoint(ptr(0.8), ptr(-0.8))
	if x != 1 || y != 0 {
		t.Fatalf("clamped relative point = %v,%v, want 1,0", x, y)
	}
}

func ptr(value float64) *float64 {
	return &value
}
