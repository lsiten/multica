//go:build darwin || linux

package hostclient

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func mediaClient(t *testing.T, mode string) (*Client, []native.SourceDescriptor) {
	t.Helper()
	cfg := testConfig(t, mode)
	cfg.Media = true
	c, err := Start(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	key := protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "workspace", RuntimeID: "runtime", UID: uint32(os.Getuid())}
	sources, err := c.Sources(t.Context(), key)
	if err != nil || len(sources) != 2 {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	t.Logf("helper pid=%d; FD3/4/5 authenticated; sources=%d", c.cmd.Process.Pid, len(sources))
	return c, sources
}
func openMedia(t *testing.T, c *Client, source native.SourceDescriptor) *Stream {
	t.Helper()
	s, err := c.OpenStream(t.Context(), EncodedSelection{Source: source, Width: 1280, Height: 720, FPS: 30, Bitrate: 4000000, MaxLevelIDC: 31})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func nextMedia(t *testing.T, s *Stream) native.MediaSample {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	sample, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return sample
}
func TestMediaDemuxCancellationAndCloseIsolation(t *testing.T) {
	// Given
	c, sources := mediaClient(t, "media")
	a, b := openMedia(t, c, sources[0]), openMedia(t, c, sources[1])
	if nextMedia(t, a).DisplayID != 1 || nextMedia(t, b).DisplayID != 2 {
		t.Fatal("crossed sources")
	}
	// When
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Then
	if err := b.ForceKeyframe(); err != nil {
		t.Fatal(err)
	}
	if sample := nextMedia(t, b); sample.PTSNanos != 100 || !sample.KeyFrame {
		t.Fatal(sample)
	}
	if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.cmd.ProcessState == nil {
		t.Fatal("helper not reaped")
	}
	t.Log("canceled Next and closed stream A; stream B delivered IDR; control responsive; helper reaped")
}
func TestMediaFailuresWakeReaders(t *testing.T) {
	for _, mode := range []string{"media-eof", "media-malformed", "media-unknown", "media-stale", "media-display", "media-generation", "media-epoch"} {
		t.Run(mode, func(t *testing.T) {
			c, sources := mediaClient(t, mode)
			a, b := openMedia(t, c, sources[0]), openMedia(t, c, sources[1])
			nextMedia(t, a)
			nextMedia(t, b)
			if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := a.Next(ctx)
			want := native.ErrProtocol
			if mode == "media-eof" {
				want = io.EOF
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
			if mode == "media-stale" || mode == "media-display" || mode == "media-generation" || mode == "media-epoch" {
				if err := b.ForceKeyframe(); err != nil {
					t.Fatal(err)
				}
				nextMedia(t, b)
			} else {
				if _, err := b.Next(ctx); !errors.Is(err, want) {
					t.Fatalf("sibling: %v", err)
				}
			}
			t.Logf("terminal error=%v; no stale frame delivered", err)
		})
	}
}
func TestMediaOverflowResumesOnlyAtIDR(t *testing.T) {
	c, sources := mediaClient(t, "media-overflow")
	a, b := openMedia(t, c, sources[0]), openMedia(t, c, sources[1])
	nextMedia(t, a)
	nextMedia(t, b)
	if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
		t.Fatal(err)
	}
	if sample := nextMedia(t, b); sample.PTSNanos != 99 {
		t.Fatal(sample)
	}
	sample := nextMedia(t, a)
	if !sample.KeyFrame || sample.PTSNanos != 100 {
		t.Fatalf("broken GOP delivered: %+v", sample)
	}
	t.Log("slow stream queue overflow: dependent frames discarded; sibling progressed; recovered at requested IDR")
}
func TestMediaPermissionErrorIsSafe(t *testing.T) {
	c, sources := mediaClient(t, "media-denied")
	_, err := c.OpenStream(t.Context(), EncodedSelection{Source: sources[0], Width: 1280, Height: 720, FPS: 30, Bitrate: 4000000, MaxLevelIDC: 31})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "screen_recording_denied" {
		t.Fatal(err)
	}
}

func TestMediaRejectsMismatchedStartDescriptor(t *testing.T) {
	c, sources := mediaClient(t, "media-response")
	_, err := c.OpenStream(t.Context(), EncodedSelection{Source: sources[0], Width: 1280, Height: 720, FPS: 30, Bitrate: 4000000, MaxLevelIDC: 31})
	if !errors.Is(err, native.ErrProtocol) {
		t.Fatal(err)
	}
}
func TestMediaBlockedNextCancellation(t *testing.T) {
	c, sources := mediaClient(t, "media")
	s := openMedia(t, c, sources[0])
	nextMedia(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := s.Next(ctx); result <- err }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next remained blocked")
	}
	if err := s.ForceKeyframe(); err != nil {
		t.Fatal(err)
	}
	nextMedia(t, s)
}

func TestMediaTerminalIsScopedToStream(t *testing.T) {
	c, sources := mediaClient(t, "media-terminal")
	a, b := openMedia(t, c, sources[0]), openMedia(t, c, sources[1])
	nextMedia(t, a)
	nextMedia(t, b)
	if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := a.Next(ctx)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "source_gone" {
		t.Fatalf("terminal got %v", err)
	}
	if err := b.ForceKeyframe(); err != nil {
		t.Fatal(err)
	}
	nextMedia(t, b)
	t.Log("source_gone terminal woke only stream A; sibling B delivered IDR")
}

func TestMediaTerminalReasons(t *testing.T) {
	for _, scenario := range []struct{ mode, code string }{{"media-terminal-closed", ""}, {"media-terminal-denied", "screen_recording_denied"}, {"media-terminal-unavailable", "capture_unavailable"}} {
		t.Run(scenario.mode, func(t *testing.T) {
			c, sources := mediaClient(t, scenario.mode)
			s := openMedia(t, c, sources[0])
			nextMedia(t, s)
			if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := s.Next(ctx)
			if scenario.code == "" {
				if !errors.Is(err, io.EOF) {
					t.Fatal(err)
				}
			} else {
				var remote *RemoteError
				if !errors.As(err, &remote) || remote.Code != scenario.code {
					t.Fatal(err)
				}
			}
		})
	}
}
