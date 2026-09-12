package mirror

import (
	"encoding/binary"
	"fmt"
)

const (
	packetKindHeader byte = 1
	packetKindChunk  byte = 2

	frameChunkPayloadLimit = 12 * 1024
	frameHeaderSize        = 1 + 4 + 2 + 4 + 4
	frameChunkHeaderSize   = 1 + 4 + 2
)

// FramePackets serializes a complete frame into ordered WebRTC data-channel
// packets. The header always precedes its bounded JPEG chunks.
func FramePackets(frame Frame) ([][]byte, error) {
	if frame.ID == 0 {
		return nil, fmt.Errorf("mirror: frame id is required")
	}
	if frame.Width <= 0 || frame.Height <= 0 {
		return nil, fmt.Errorf("mirror: frame dimensions must be positive")
	}
	if len(frame.JPEG) == 0 {
		return nil, fmt.Errorf("mirror: JPEG is required")
	}
	chunkCount := (len(frame.JPEG) + frameChunkPayloadLimit - 1) / frameChunkPayloadLimit
	if chunkCount > int(^uint16(0)) {
		return nil, fmt.Errorf("mirror: frame has too many chunks")
	}
	packets := make([][]byte, 0, chunkCount+1)
	header := make([]byte, frameHeaderSize)
	header[0] = packetKindHeader
	binary.BigEndian.PutUint32(header[1:5], frame.ID)
	binary.BigEndian.PutUint16(header[5:7], uint16(chunkCount))
	binary.BigEndian.PutUint32(header[7:11], uint32(frame.Width))
	binary.BigEndian.PutUint32(header[11:15], uint32(frame.Height))
	packets = append(packets, header)
	for chunkIndex := 0; chunkIndex < chunkCount; chunkIndex++ {
		start := chunkIndex * frameChunkPayloadLimit
		end := start + frameChunkPayloadLimit
		if end > len(frame.JPEG) {
			end = len(frame.JPEG)
		}
		packet := make([]byte, frameChunkHeaderSize+end-start)
		packet[0] = packetKindChunk
		binary.BigEndian.PutUint32(packet[1:5], frame.ID)
		binary.BigEndian.PutUint16(packet[5:7], uint16(chunkIndex))
		copy(packet[frameChunkHeaderSize:], frame.JPEG[start:end])
		packets = append(packets, packet)
	}
	return packets, nil
}
