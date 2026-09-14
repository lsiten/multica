package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestMediaRejectsOversizedPayloadBeforeAllocation(t *testing.T) {
	// Given
	header := make([]byte, 120)
	copy(header, []byte("MVSC"))
	binary.BigEndian.PutUint16(header[4:], 1)
	binary.BigEndian.PutUint32(header[8:], 8*1024*1024+1)
	// When
	_, err := ReadMediaSample(bytes.NewReader(header))
	// Then
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v", err)
	}
}

func TestMediaRoundTripPreservesExactSource(t *testing.T) {
	// Given
	want := MediaSample{StreamID: "00112233445566778899aabbccddeeff", Epoch: protocol.VscreenEpoch{NativeEpoch: strings.Repeat("a", 64), DisplayGeneration: strings.Repeat("b", 64), GeometryRevision: 7}, DisplayID: 42, PTSNanos: 999, DurationNanos: 33333333, KeyFrame: true, AnnexB: []byte{0, 0, 0, 1, 0x65}}
	var wire bytes.Buffer
	// When
	if err := WriteMediaSample(&wire, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMediaSample(&wire)
	if err != nil {
		t.Fatal(err)
	}
	// Then
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("got %+v", got)
	}
}

func TestMediaRejectsTruncatedPayload(t *testing.T) {
	// Given
	sample := MediaSample{StreamID: strings.Repeat("a", 32), Epoch: protocol.VscreenEpoch{NativeEpoch: strings.Repeat("b", 64), DisplayGeneration: strings.Repeat("c", 64), GeometryRevision: 1}, DisplayID: 2, DurationNanos: 1, AnnexB: []byte{1, 2}}
	var wire bytes.Buffer
	if err := WriteMediaSample(&wire, sample); err != nil {
		t.Fatal(err)
	}
	// When
	_, err := ReadMediaSample(bytes.NewReader(wire.Bytes()[:wire.Len()-1]))
	// Then
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v", err)
	}
}

func TestMediaTerminalDoesNotStopSiblingStream(t *testing.T) {
	// Given: two source identities sharing the actual host media writer.
	parent, child := net.Pipe()
	defer parent.Close()
	defer child.Close()
	epoch := protocol.VscreenEpoch{NativeEpoch: strings.Repeat("a", 64), DisplayGeneration: strings.Repeat("b", 64), GeometryRevision: 1}
	host := newCaptureHost(true, [32]byte{})
	host.media = child
	first := &capturedStream{descriptor: CaptureDescriptor{StreamID: strings.Repeat("1", 32), Source: SourceDescriptor{DisplayID: 10}}, err: ErrUnavailable}
	sibling := MediaSample{StreamID: strings.Repeat("2", 32), Epoch: epoch, DisplayID: 20, PTSNanos: 1, DurationNanos: 1, AnnexB: []byte{0, 0, 0, 1, 0x65}, KeyFrame: true}
	done := make(chan error, 1)
	// When: one stream terminates, followed by another stream's video.
	go func() {
		if err := host.emitTerminal(first, epoch); err != nil {
			done <- err
			return
		}
		done <- host.writeSample(sibling)
	}()
	terminal, err := ReadMediaSample(parent)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := ReadMediaSample(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Then: terminal is typed, payload-free video-wise, and sibling source remains usable.
	if terminal.Kind != MediaTerminal || terminal.TerminalReason != TerminalSourceGone || terminal.StreamID != first.descriptor.StreamID || terminal.DisplayID != 10 || terminal.Epoch != epoch || len(terminal.AnnexB) != 0 {
		t.Fatalf("invalid terminal %+v", terminal)
	}
	if continued.Kind != MediaVideo || continued.StreamID != sibling.StreamID || !bytes.Equal(continued.AnnexB, sibling.AnnexB) {
		t.Fatalf("sibling corrupted %+v", continued)
	}
}

func TestMediaRejectsTerminalMasqueradingAsVideo(t *testing.T) {
	// Given
	sample := MediaSample{Kind: MediaTerminal, TerminalReason: TerminalClosed, StreamID: strings.Repeat("a", 32), Epoch: protocol.VscreenEpoch{NativeEpoch: strings.Repeat("b", 64), DisplayGeneration: strings.Repeat("c", 64), GeometryRevision: 1}, DisplayID: 1, AnnexB: []byte{0, 0, 0, 1}}
	// When
	err := WriteMediaSample(io.Discard, sample)
	// Then
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v", err)
	}
}

func TestMediaRejectsUnknownTerminalReason(t *testing.T) {
	// Given: a valid terminal header with an untrusted reason substituted on the wire.
	sample := MediaSample{Kind: MediaTerminal, TerminalReason: TerminalClosed, StreamID: strings.Repeat("a", 32), Epoch: protocol.VscreenEpoch{NativeEpoch: strings.Repeat("b", 64), DisplayGeneration: strings.Repeat("c", 64), GeometryRevision: 1}, DisplayID: 1}
	var buffer bytes.Buffer
	if err := WriteMediaSample(&buffer, sample); err != nil {
		t.Fatal(err)
	}
	wire := buffer.Bytes()
	copy(wire[MediaHeaderBytes:], "secret")
	// When
	_, err := ReadMediaSample(bytes.NewReader(wire))
	// Then
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v", err)
	}
}
