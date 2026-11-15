//go:build !darwin || !cgo

package capture

import (
	"context"
	"errors"
	"testing"
)

func TestUnsupportedOpenNeverClaimsCapture(t *testing.T) {
	// Given / When: a valid source configuration on an unsupported build.
	stream, err := Open(context.Background(), Config{DisplayID: 1, Width: 1600, Height: 900, FPS: 30, Bitrate: 4000000})
	// Then
	if stream != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("stream=%v error=%v", stream, err)
	}
}
