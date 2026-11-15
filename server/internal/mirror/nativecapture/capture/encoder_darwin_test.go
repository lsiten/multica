//go:build darwin && cgo && nativeintegration

package capture

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVideoToolboxSyntheticEncode(t *testing.T) {
	encodeSynthetic(t, Config{DisplayID: 1, Width: 1600, Height: 900, FPS: 30, Bitrate: 4000000, MaxLevelIDC: 40})
}
func TestVideoToolboxNegotiated720p(t *testing.T) {
	evidence := os.Getenv("MULTICA_VSCREEN_SMOKE_EVIDENCE")
	if evidence == "" {
		t.Fatal("explicit evidence directory required")
	}
	directory := filepath.Join(evidence, "encoder-720p")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_VSCREEN_SMOKE_EVIDENCE", directory)
	encodeSynthetic(t, Config{DisplayID: 1, Width: 1280, Height: 720, FPS: 30, Bitrate: 4000000, MaxLevelIDC: 31})
}
func encodeSynthetic(t *testing.T, config Config) {
	// Given: synthesized task-owned BGRA buffers, with no display or screen recording.
	evidence := os.Getenv("MULTICA_VSCREEN_SMOKE_EVIDENCE")
	if evidence == "" {
		t.Fatal("explicit evidence directory required")
	}
	stream, err := newEncoderFixture(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	defer func() {
		if err := stream.Close(context.WithoutCancel(ctx)); err != nil {
			t.Error(err)
		}
	}()
	type receipt struct {
		Index    uint32  `json:"index"`
		PTS      int64   `json:"pts_ns"`
		Duration int64   `json:"duration_ns"`
		Keyframe bool    `json:"keyframe"`
		Bytes    int     `json:"bytes"`
		NALTypes []uint8 `json:"nal_types"`
	}
	receipts := make([]receipt, 0, 6)
	var encoded []byte
	// When: encode six independent inputs, explicitly forcing another IDR midway.
	for index := uint32(0); index < 6; index++ {
		if index == 3 {
			if err := stream.ForceKeyframe(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := stream.fixtureFrame(index); err != nil {
			t.Fatal(err)
		}
		sample, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var types []uint8
		for i := 0; i+4 < len(sample.AnnexB); i++ {
			if sample.AnnexB[i] == 0 && sample.AnnexB[i+1] == 0 && sample.AnnexB[i+2] == 0 && sample.AnnexB[i+3] == 1 {
				types = append(types, sample.AnnexB[i+4]&31)
			}
		}
		receipt := receipt{index, sample.PTSNanos, sample.DurationNanos, sample.KeyFrame, len(sample.AnnexB), types}
		receipts = append(receipts, receipt)
		encoded = append(encoded, sample.AnnexB...)
		// Then: true Annex-B, monotonic PTS, and parameter sets preceding each requested IDR.
		if len(types) == 0 || sample.DurationNanos <= 0 || (index > 0 && sample.PTSNanos <= receipts[index-1].PTS) {
			t.Fatalf("invalid sample %+v", receipt)
		}
		if index == 0 || index == 3 {
			if !sample.KeyFrame || len(types) < 3 || types[0] != 7 || types[1] != 8 || types[len(types)-1] != 5 {
				t.Fatalf("missing SPS/PPS/IDR %+v", receipt)
			}
		}
	}
	body, err := json.MarshalIndent(receipts, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "task-9-encoder-synthetic.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "task-9-encoder-synthetic.h264"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	stats, err := stream.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.PeakInFlight > 2 || stats.PeakQueued > 3 || stats.RetainedInput > 1 {
		t.Fatalf("unbounded buffers %+v", stats)
	}
	statsJSON, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "task-9-encoder-stats.json"), statsJSON, 0600); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Next(ctx); err != ErrClosed {
		t.Fatalf("closed encoder yielded %v", err)
	}
}

func TestVideoToolboxBoundedBurstAndClose(t *testing.T) {
	// Given: a synthetic source and a stalled consumer.
	evidence := os.Getenv("MULTICA_VSCREEN_SMOKE_EVIDENCE")
	if evidence == "" {
		t.Fatal("explicit evidence directory required")
	}
	stream, err := newEncoderFixture(Config{DisplayID: 1, Width: 320, Height: 180, FPS: 30, Bitrate: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() {
		if err := stream.Close(context.WithoutCancel(ctx)); err != nil {
			t.Error(err)
		}
	}()
	// When: a burst replaces pending buffers without reading encoded output.
	for index := uint32(0); index < 200; index++ {
		if err := stream.fixtureFrame(index); err != nil {
			t.Fatal(err)
		}
	}
	var stats Stats
	for {
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		stats, err = stream.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if stats.RetainedInput == 0 && stats.InFlight == 0 {
			break
		}
	}
	// Then: both raw and encoded residency stay bounded, with actual drops observable.
	if stats.PeakInFlight > 2 || stats.PeakQueued > 3 || stats.RetainedInput > 1 || stats.DroppedInput+stats.DroppedOutput == 0 {
		t.Fatalf("invalid buffer residency %+v", stats)
	}
	body, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "task-4-native-buffer-burst.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := stream.ForceKeyframe(ctx); err != ErrClosed {
		t.Fatalf("closed native session remained writable: %v", err)
	}
}

func TestCaptureDeniedPermissionNeverStartsStream(t *testing.T) {
	// Given: explicitly verify this machine has not granted recording consent.
	granted, err := PermissionGranted()
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		t.Fatal("denial fixture requires permission denied; no capture attempted")
	}
	// When
	stream, err := Open(context.Background(), Config{DisplayID: 999999, Width: 1600, Height: 900, FPS: 30, Bitrate: 4000000})
	// Then: refusal occurs before display lookup or stream creation.
	if stream != nil || err != ErrPermission {
		t.Fatalf("stream=%v error=%v", stream, err)
	}
	evidence := os.Getenv("MULTICA_VSCREEN_SMOKE_EVIDENCE")
	if evidence == "" {
		t.Fatal("explicit evidence directory required")
	}
	if err := os.WriteFile(filepath.Join(evidence, "task-4-native-permission.json"), []byte("{\"screen_recording\":false,\"open_error\":\"permission_denied\",\"stream_created\":false}\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
