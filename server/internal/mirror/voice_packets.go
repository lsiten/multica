package mirror

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const voicePacketHeader = 12
const voicePacketLimit = 16 * 1024
const voicePacketMagic = 0x4d564331 // MVC1

// Ordered reliable channels carry one bounded recording at a time. Offsets
// reject missing, duplicate and interleaved fragments before transcription.
type voiceAssembly struct {
	data     []byte
	total    int
	deadline time.Time
}

func (a *voiceAssembly) push(packet []byte, now time.Time) ([]byte, error) {
	invalid := func() ([]byte, error) {
		*a = voiceAssembly{}
		return nil, errors.New("invalid voice packet")
	}
	if len(packet) <= voicePacketHeader || len(packet) > voicePacketLimit || binary.BigEndian.Uint32(packet[:4]) != voicePacketMagic {
		return invalid()
	}
	total := int(binary.BigEndian.Uint32(packet[4:8]))
	offset := int(binary.BigEndian.Uint32(packet[8:12]))
	payload := packet[voicePacketHeader:]
	if total < 1 || total > protocol.MaxMirrorInputBytes*64 || offset > total || len(payload) > total-offset {
		return invalid()
	}
	if offset == 0 {
		if a.data != nil && now.Before(a.deadline) {
			return invalid()
		}
		*a = voiceAssembly{data: make([]byte, 0, total), total: total, deadline: now.Add(30 * time.Second)}
	}
	if a.data == nil || !now.Before(a.deadline) || total != a.total || offset != len(a.data) {
		return invalid()
	}
	a.data = append(a.data, payload...)
	if len(a.data) != a.total {
		return nil, nil
	}
	complete := a.data
	*a = voiceAssembly{}
	return complete, nil
}
