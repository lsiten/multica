package mirror

import (
	"bytes"
	"testing"
)

func TestFramePacketsPutMetadataBeforeBoundedChunks(t *testing.T) {
	// Given
	frame := Frame{
		ID:     7,
		Width:  1280,
		Height: 720,
		JPEG:   bytes.Repeat([]byte{0x5a}, frameChunkPayloadLimit*2+1),
	}

	// When
	packets, err := FramePackets(frame)

	// Then
	if err != nil {
		t.Fatalf("packetize frame: %v", err)
	}
	if len(packets) != 4 {
		t.Fatalf("packet count = %d, want 4", len(packets))
	}
	if packets[0][0] != packetKindHeader {
		t.Fatalf("header kind = %d, want %d", packets[0][0], packetKindHeader)
	}
	for index, packet := range packets[1:] {
		if packet[0] != packetKindChunk {
			t.Fatalf("chunk %d kind = %d, want %d", index, packet[0], packetKindChunk)
		}
		if len(packet) > frameChunkPayloadLimit+frameChunkHeaderSize {
			t.Fatalf("chunk %d len = %d, exceeds limit", index, len(packet))
		}
	}
}
