package native

import (
	"context"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"reflect"
	"testing"
)

type exclusionCapture struct {
	updates [][]uint32
	failure error
}

func (f *exclusionCapture) Next(context.Context) (capture.Sample, error) {
	return capture.Sample{}, capture.ErrClosed
}
func (f *exclusionCapture) Close(context.Context) error         { return nil }
func (f *exclusionCapture) ForceKeyframe(context.Context) error { return nil }
func (f *exclusionCapture) Stats() (capture.Stats, error)       { return capture.Stats{}, nil }
func (f *exclusionCapture) UpdateExclusions(ctx context.Context, ids []uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.updates = append(f.updates, append([]uint32(nil), ids...))
	return f.failure
}
func TestCaptureExclusionsUpdateExistingRealStreamsOnly(t *testing.T) {
	h := newCaptureHost(true, [32]byte{})
	physical, virtual, closed := &exclusionCapture{}, &exclusionCapture{}, &exclusionCapture{}
	add := func(id string, kind protocol.MirrorSourceKind, stream *exclusionCapture, done bool) {
		entry := &capturedStream{stream: stream, done: make(chan struct{})}
		entry.descriptor.Source.Source.Kind = kind
		if done {
			close(entry.done)
		}
		h.streams[id] = entry
	}
	add("physical", protocol.MirrorSourcePhysical, physical, false)
	add("virtual", protocol.MirrorSourceVirtual, virtual, false)
	add("closed", protocol.MirrorSourceSystem, closed, true)
	for _, ids := range [][]uint32{{41, 42}, {42}, {}} {
		if err := h.updateExclusions(t.Context(), ids); err != nil {
			t.Fatal(err)
		}
	}
	if len(physical.updates) != 3 || !reflect.DeepEqual(physical.updates[0], []uint32{41, 42}) || len(physical.updates[2]) != 0 {
		t.Fatal("existing filter not updated on open/close")
	}
	if len(virtual.updates) != 0 || len(closed.updates) != 0 {
		t.Fatal("updated unrelated/closed source")
	}
	if len(h.streams) != 3 {
		t.Fatal("replaced capture streams")
	}
	if h.updateExclusions(t.Context(), []uint32{1, 1}) == nil || h.updateExclusions(t.Context(), []uint32{0}) == nil {
		t.Fatal("invalid exclusions accepted")
	}
}

func TestCaptureExclusionsClosingRaceDoesNotBlockSiblingUpdate(t *testing.T) {
	h := newCaptureHost(true, [32]byte{})
	closing := &exclusionCapture{failure: capture.ErrClosed}
	sibling := &exclusionCapture{}
	for id, stream := range map[string]*exclusionCapture{"closing": closing, "sibling": sibling} {
		entry := &capturedStream{stream: stream, done: make(chan struct{})}
		entry.descriptor.Source.Source.Kind = protocol.MirrorSourcePhysical
		h.streams[id] = entry
	}
	if err := h.updateExclusions(t.Context(), []uint32{41}); err != nil {
		t.Fatal(err)
	}
	if len(sibling.updates) != 1 {
		t.Fatal("sibling was starved by concurrent close")
	}
	if err := h.updateExclusions(t.Context(), []uint32{41}); err != nil {
		t.Fatal(err)
	}
	if len(sibling.updates) != 1 {
		t.Fatal("unchanged filter was redundantly replaced")
	}
}
