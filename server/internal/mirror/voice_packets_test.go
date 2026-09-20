package mirror

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func voicePacket(total, offset int, payload []byte) []byte {
	packet := make([]byte, voicePacketHeader+len(payload))
	binary.BigEndian.PutUint32(packet, voicePacketMagic)
	binary.BigEndian.PutUint32(packet[4:], uint32(total))
	binary.BigEndian.PutUint32(packet[8:], uint32(offset))
	copy(packet[12:], payload)
	return packet
}

func TestVoiceAssemblyLargeRecording(t *testing.T) {
	var assembly voiceAssembly
	audio := bytes.Repeat([]byte("audio"), 30_000)
	now := time.Now()
	for offset := 0; offset < len(audio); {
		end := min(offset+voicePacketLimit-voicePacketHeader, len(audio))
		got, err := assembly.push(voicePacket(len(audio), offset, audio[offset:end]), now)
		if err != nil {
			t.Fatal(err)
		}
		if end == len(audio) {
			if !bytes.Equal(got, audio) || assembly.data != nil {
				t.Fatal("recording was corrupted or retained after completion")
			}
		} else if got != nil {
			t.Fatal("partial recording reached transcriber")
		}
		offset = end
	}
}

func TestVoiceAssemblyRejectsInvalidPackets(t *testing.T) {
	for _, scenario := range []string{"duplicate", "gap", "total_changed", "expired", "oversized_total", "oversized_packet", "empty", "magic"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now()
			var assembly voiceAssembly
			if _, err := assembly.push(voicePacket(4, 0, []byte{1, 2}), now); err != nil {
				t.Fatal(err)
			}
			packet := voicePacket(4, 2, []byte{3, 4})
			switch scenario {
			case "duplicate":
				packet = voicePacket(4, 0, []byte{1, 2})
			case "gap":
				packet = voicePacket(4, 3, []byte{4})
			case "total_changed":
				packet = voicePacket(5, 2, []byte{3, 4})
			case "expired":
				now = now.Add(31 * time.Second)
			case "oversized_total":
				packet = voicePacket(2*1024*1024, 0, []byte{1})
			case "oversized_packet":
				packet = voicePacket(20_000, 0, make([]byte, 20_000))
			case "empty":
				packet = nil
			case "magic":
				packet[0] = 0
			}
			if got, err := assembly.push(packet, now); err == nil || got != nil || assembly.data != nil {
				t.Fatal("invalid packet accepted or partial audio retained")
			}
		})
	}
}
