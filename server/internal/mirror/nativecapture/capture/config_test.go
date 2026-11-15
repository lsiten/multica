package capture

import (
	"context"
	"errors"
	"testing"
)

func TestOpenRejectsInvalidGeometryBeforeNativeAccess(t *testing.T) {
	// Given
	config := Config{DisplayID: 1, Width: 0, Height: 900, FPS: 30, Bitrate: 4000000}
	// When
	_, err := Open(context.Background(), config)
	// Then
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenHonorsCancellationBeforeNativeAccess(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := Config{DisplayID: 1, Width: 1600, Height: 900, FPS: 30, Bitrate: 4000000}
	// When
	_, err := Open(ctx, config)
	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestConfigRejectsInvalidBounds(t *testing.T) {
	for _, config := range []Config{
		{DisplayID: 0, Width: 1600, Height: 900, FPS: 30, Bitrate: 4000000},
		{DisplayID: 1, Width: 4098, Height: 900, FPS: 30, Bitrate: 4000000},
		{DisplayID: 1, Width: 1601, Height: 900, FPS: 30, Bitrate: 4000000},
		{DisplayID: 1, Width: 1600, Height: 900, FPS: 61, Bitrate: 4000000},
		{DisplayID: 1, Width: 1600, Height: 900, FPS: 30, Bitrate: 99999},
		{DisplayID: 1, Width: 1600, Height: 900, FPS: 30, Bitrate: 4000000, ExcludedWindowIDs: []uint32{0}},
	} {
		// Given / When: invalid external capture configuration.
		err := config.validate()
		// Then
		if !errors.Is(err, ErrConfig) {
			t.Fatalf("accepted %+v", config)
		}
	}
}
