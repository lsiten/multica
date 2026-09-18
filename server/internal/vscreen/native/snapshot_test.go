package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestSnapshotCodecRoundTripAndVideoInterleave(t *testing.T) {
	snapshot := MediaSample{Kind: MediaSnapshot, StreamID: strings.Repeat("1", 32), Epoch: protocol.VscreenEpoch{NativeEpoch: strings.Repeat("a", 64), DisplayGeneration: strings.Repeat("b", 64), GeometryRevision: 1}, DisplayID: 3, SnapshotOffset: 0, SnapshotTotal: 9, PNG: []byte{137, 80, 78, 71, 13, 10, 26, 10, 0}}
	video := MediaSample{StreamID: strings.Repeat("2", 32), Epoch: snapshot.Epoch, DisplayID: 4, DurationNanos: 1, AnnexB: []byte{0, 0, 0, 1, 0x65}}
	var wire bytes.Buffer
	for _, s := range []MediaSample{snapshot, video, snapshot} {
		if err := WriteMediaSample(&wire, s); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []MediaSample{snapshot, video, snapshot} {
		got, err := ReadMediaSample(&wire)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("media mismatch %+v %v", got, err)
		}
	}
}
func TestSnapshotCodecRejectsMalformedBoundsBeforePayloadAllocation(t *testing.T) {
	s := MediaSample{Kind: MediaSnapshot, StreamID: strings.Repeat("1", 32), Epoch: protocol.VscreenEpoch{NativeEpoch: strings.Repeat("a", 64), DisplayGeneration: strings.Repeat("b", 64), GeometryRevision: 1}, DisplayID: 3, SnapshotTotal: 8, PNG: []byte("12345678")}
	var b bytes.Buffer
	if err := WriteMediaSample(&b, s); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func([]byte){func(h []byte) { binary.BigEndian.PutUint32(h[8:], SnapshotChunkBytes+1) }, func(h []byte) { binary.BigEndian.PutUint64(h[20:], MaxMediaPayloadBytes+1) }, func(h []byte) { binary.BigEndian.PutUint64(h[12:], 7) }, func(h []byte) { binary.BigEndian.PutUint16(h[6:], 5) }} {
		header := append([]byte(nil), b.Bytes()[:MediaHeaderBytes]...)
		change(header)
		if _, err := ReadMediaSample(bytes.NewReader(header)); !errors.Is(err, ErrProtocol) {
			t.Fatalf("malformed header accepted: %v", err)
		}
	}
	if _, err := ReadMediaSample(bytes.NewReader(b.Bytes()[:b.Len()-1])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated PNG accepted: %v", err)
	}
	s.AnnexB = []byte{1}
	if WriteMediaSample(io.Discard, s) == nil {
		t.Fatal("PNG treated as Annex-B")
	}
}
