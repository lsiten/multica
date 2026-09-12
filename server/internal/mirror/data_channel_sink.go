package mirror

import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v4"
)

type dataChannelSink struct {
	channel *webrtc.DataChannel
}

func (s dataChannelSink) SendFrame(frame Frame) error {
	packets, err := FramePackets(frame)
	if err != nil {
		return err
	}
	for _, packet := range packets {
		if err := s.channel.Send(packet); err != nil {
			return fmt.Errorf("mirror: send frame packet: %w", err)
		}
	}
	return nil
}

func (s dataChannelSink) SendControl(message ControlMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("mirror: marshal control message: %w", err)
	}
	if err := s.channel.SendText(string(payload)); err != nil {
		return fmt.Errorf("mirror: send control message: %w", err)
	}
	return nil
}
