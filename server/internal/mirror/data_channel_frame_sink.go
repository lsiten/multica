package mirror

import (
	"encoding/json"

	"github.com/pion/webrtc/v4"
)

type channelFrameSink struct{ channel *webrtc.DataChannel }

func (s channelFrameSink) SendFrame(frame Frame) error {
	if s.channel == nil || s.channel.ReadyState() != webrtc.DataChannelStateOpen {
		return ErrViewerClosed
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return s.channel.Send(payload)
}

func (s channelFrameSink) SendCaptureError(message CaptureErrorMessage) error {
	if s.channel == nil || s.channel.ReadyState() != webrtc.DataChannelStateOpen {
		return ErrViewerClosed
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return s.channel.SendText(string(payload))
}
